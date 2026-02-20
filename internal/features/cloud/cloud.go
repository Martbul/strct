package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/strct-org/strct-agent/internal/config"
	"github.com/strct-org/strct-agent/internal/httputil"
	"github.com/strct-org/strct-agent/internal/humanize"
	"github.com/strct-org/strct-agent/internal/netx"
	"github.com/strct-org/strct-agent/internal/platform/disk"
)

// Cloud manages local file storage and exposes it over HTTP.
// Construct via NewFromConfig — do not use New directly from main.
type Cloud struct {
	StartTime time.Time
	DataDir   string
	Port      int
	IsDev     bool
}

// StatusResponse is the JSON shape returned by /api/status.
type StatusResponse struct {
	Uptime   int64  `json:"uptime"`
	IP       string `json:"ip"`
	Used     uint64 `json:"used"`
	Total    uint64 `json:"total"`
	IsOnline bool   `json:"isOnline"`
}

// FilesResponse is the JSON shape returned by /api/files.
type FilesResponse struct {
	Files []FileItem `json:"files"`
}

// FileItem represents a single file or folder entry.
type FileItem struct {
	Name       string `json:"name"`
	Size       string `json:"size"`
	Type       string `json:"type"`
	ModifiedAt string `json:"modifiedAt"`
}

// New is the base constructor. Prefer NewFromConfig in application code.
func New(dataDir string, port int, isDev bool) *Cloud {
	return &Cloud{
		DataDir: dataDir,
		Port:    port,
		IsDev:   isDev,
	}
}

func NewFromConfig(cfg *config.Config) (*Cloud, error) {
	c := New(cfg.DataDir, 8080, cfg.IsDev)
	if err := c.initFileSystem(); err != nil {
		return nil, err
	}
	return c, nil
}

func NewFromConfig_Test(dataDir string) (*Cloud, error) {
	c := New(dataDir, 8080, true)
	if err := c.initFileSystem(); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Cloud) Start(_ context.Context) error {
	return nil
}

func (s *Cloud) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/files", s.handleFiles)
	mux.HandleFunc("POST /api/mkdir", s.handleMkdir)
	mux.HandleFunc("DELETE /api/delete", s.handleDelete)
	mux.HandleFunc("POST /strct_agent/fs/upload", s.handleUpload)
	mux.Handle("/files/", http.StripPrefix("/files/", http.FileServer(http.Dir(s.DataDir))))
}

// initFileSystem detects storage, mounts SSD if present, then ensures the
// data directory exists. Unexported because it must be called exactly once
// by NewFromConfig — callers should never call it directly.
func (s *Cloud) initFileSystem() error {
	// SSD detection is hardware-only. In dev mode we always use the
	// configured DataDir (./data) so local test files remain visible.
	if !s.IsDev {
		candidates := []string{"/dev/nvme0n1", "/dev/sda"}
		const ssdMountPoint = "/mnt/strct_data"

		for _, devicePath := range candidates {
			if _, err := os.Stat(devicePath); err != nil {
				continue // device not present on this machine
			}

			d := &disk.RealDisk{DevicePath: devicePath}
			if err := d.EnsureMounted(ssdMountPoint); err != nil {
				slog.Warn("storage: device detected but could not mount (unformatted?)",
					"device", devicePath, "err", err)
				continue
			}

			slog.Info("storage: SSD selected",
				"device", devicePath,
				"mount", ssdMountPoint,
			)
			s.DataDir = ssdMountPoint
			break
		}
	}

	// Log which storage path is actually in use
	slog.Info("storage: active path", "dir", s.DataDir)

	// Resolve to absolute path so later joins are unambiguous
	absPath, err := filepath.Abs(s.DataDir)
	if err != nil {
		absPath = filepath.Clean(s.DataDir)
	}
	s.DataDir = absPath

	if err := os.MkdirAll(s.DataDir, 0755); err != nil {
		return fmt.Errorf("cloud: could not create data directory %s: %w", s.DataDir, err)
	}

	s.StartTime = time.Now()
	return nil
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

func (s *Cloud) handleStatus(w http.ResponseWriter, r *http.Request) {
	realFree, _ := disk.GetFreeDiskSpace(s.DataDir)
	userUsed, err := disk.GetDirSize(s.DataDir)
	if err != nil {
		slog.Error("cloud: failed to calculate dir size", "err", err)
	}

	httputil.OK(w, StatusResponse{
		IsOnline: true,
		Used:     userUsed,
		Total:    userUsed + realFree,
		IP:       netx.GetOutboundIP(),
		Uptime:   int64(time.Since(s.StartTime).Seconds()),
	})
}

func (s *Cloud) handleFiles(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Query().Get("path")
	fullPath, err := secureJoin(s.DataDir, reqPath)
	if err != nil {
		httputil.Forbidden(w)
		return
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		// Directory might not exist yet — return empty list, not an error
		httputil.OK(w, FilesResponse{Files: []FileItem{}})
		return
	}

	var fileList []FileItem
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		fileType := "file"
		if e.IsDir() {
			fileType = "folder"
		}
		fileList = append(fileList, FileItem{
			Name:       e.Name(),
			Size:       humanize.Bytes(info.Size()),
			Type:       fileType,
			ModifiedAt: info.ModTime().Format(time.RFC3339),
		})
	}

	httputil.OK(w, FilesResponse{Files: fileList})
}

func (s *Cloud) handleMkdir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.BadRequest(w, "invalid JSON")
		return
	}
	if req.Name == "" || strings.Contains(req.Name, "/") || strings.Contains(req.Name, "\\") {
		httputil.BadRequest(w, "invalid folder name")
		return
	}

	parentDir, err := secureJoin(s.DataDir, req.Path)
	if err != nil {
		httputil.Forbidden(w)
		return
	}

	if err := os.Mkdir(filepath.Join(parentDir, req.Name), 0755); err != nil {
		if os.IsExist(err) {
			httputil.Error(w, http.StatusConflict, "folder already exists")
			return
		}
		slog.Error("cloud: failed to create folder", "err", err)
		httputil.InternalError(w, "could not create folder")
		return
	}

	httputil.JSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (s *Cloud) handleDelete(w http.ResponseWriter, r *http.Request) {
	targetPath := r.URL.Query().Get("path")
	fullPath, err := secureJoin(s.DataDir, targetPath)
	if err != nil {
		httputil.Forbidden(w)
		return
	}
	if fullPath == s.DataDir {
		httputil.Forbidden(w)
		return
	}
	if err := os.RemoveAll(fullPath); err != nil {
		slog.Error("cloud: failed to delete", "path", fullPath, "err", err)
		httputil.InternalError(w, "could not delete item")
		return
	}
	httputil.NoContent(w)
}

func (s *Cloud) handleUpload(w http.ResponseWriter, r *http.Request) {
	targetDir := r.URL.Query().Get("path")
	saveDir, err := secureJoin(s.DataDir, targetDir)
	if err != nil {
		httputil.Forbidden(w)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 50<<30) // 50 GB hard limit
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httputil.BadRequest(w, "could not parse multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.BadRequest(w, "invalid file field")
		return
	}
	defer file.Close()

	dst, err := os.Create(filepath.Join(saveDir, header.Filename))
	if err != nil {
		slog.Error("cloud: failed to create destination file", "err", err)
		httputil.InternalError(w, "disk error")
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		slog.Error("cloud: failed to write uploaded file", "err", err)
		httputil.InternalError(w, "upload failed")
		return
	}

	httputil.JSON(w, http.StatusCreated, map[string]string{"status": "uploaded"})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// secureJoin safely joins a user-supplied path under root, rejecting traversal.
func secureJoin(root, userPath string) (string, error) {
	if userPath == "" {
		userPath = "/"
	}
	full := filepath.Join(root, filepath.Clean(filepath.Join("/", userPath)))
	if !strings.HasPrefix(full, root) {
		return "", fmt.Errorf("path traversal attempt: %q", userPath)
	}
	return full, nil
}
