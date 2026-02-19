package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/strct-org/strct-agent/internal/config"
	"github.com/strct-org/strct-agent/internal/disk"
	"github.com/strct-org/strct-agent/internal/humanize"
	"github.com/strct-org/strct-agent/internal/netx"
)

type Cloud struct {
	StartTime time.Time
	DataDir   string
	Port      int
	IsDev     bool
}

type StatusResponse struct {
	Uptime   int64  `json:"uptime"`
	IP       string `json:"ip"`
	Used     uint64 `json:"used"`
	Total    uint64 `json:"total"`
	IsOnline bool   `json:"isOnline"`
}

type FilesResponse struct {
	Files []FileItem `json:"files"`
}

type FileItem struct {
	Name       string `json:"name"`
	Size       string `json:"size"`
	Type       string `json:"type"`
	ModifiedAt string `json:"modifiedAt"`
}

func New(dataDir string, port int, isDev bool) *Cloud {
	return &Cloud{
		DataDir: dataDir,
		Port:    port,
		IsDev:   isDev,
	}
}

func NewFromConfig(cfg *config.Config) (*Cloud, error) {
	c := New(cfg.DataDir, 8080, cfg.IsDev)
	if err := c.InitFileSystem(); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Cloud) Start(_ context.Context) error {
	return nil // Cloud has no background loop; work is done at init and via HTTP
}

func (s *Cloud) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/mkdir", s.handleMkdir)
	mux.HandleFunc("/api/delete", s.handleDelete)
	mux.HandleFunc("/strct_agent/fs/upload", s.handleUpload)
	mux.Handle("/files/", http.StripPrefix("/files/", http.FileServer(http.Dir(s.DataDir))))
}

func (s *Cloud) InitFileSystem() error {
	candidates := []string{"/dev/nvme0n1", "/dev/sda"}

	const ssdMountPoint = "/mnt/strct_data"

	ssdSelected := false

	for _, devicePath := range candidates {
		if _, err := os.Stat(devicePath); err == nil {

			//! SOFT DELETE
			// d := &disk.RealDisk{DevicePath: devicePath}
			d := disk.New(s.IsDev)

			err := d.EnsureMounted(ssdMountPoint)

			if err == nil {
				// SUCCESS: SSD is formatted and mounted
				log.Printf("------------------------------------------------")
				log.Printf("[STORAGE] PRIORITY SELECT: SSD SELECTED")
				log.Printf("[STORAGE] Device: %s", devicePath)
				log.Printf("[STORAGE] Mount:  %s", ssdMountPoint)
				log.Printf("------------------------------------------------")

				// Update the Cloud struct to use this new path
				s.DataDir = ssdMountPoint
				ssdSelected = true
				break
			} else {
				// Device exists but failed to mount (likely unformatted)
				log.Printf("[STORAGE] Detected %s but could not mount (Unformatted?): %v", devicePath, err)
			}
		}
	}

	// 2. Fallback to SD Card if no SSD was successfully mounted
	if !ssdSelected {
		log.Printf("------------------------------------------------")
		log.Printf("[STORAGE] PRIORITY SELECT: SD CARD / INTERNAL")
		log.Printf("[STORAGE] Reason: No formatted SSD found or mounted.")
		log.Printf("[STORAGE] Path:   %s", s.DataDir)
		log.Printf("------------------------------------------------")
	}

	// 3. Initialize the directory (Ensure it exists)
	// This runs on s.DataDir, which is now either the SSD path OR the original SD path
	absPath, err := filepath.Abs(s.DataDir)
	if err != nil {
		absPath = filepath.Clean(s.DataDir)
	}
	s.DataDir = absPath

	if err := os.MkdirAll(s.DataDir, 0755); err != nil {
		log.Printf("[CLOUD] Error creating data directory: %v", err)
		return err
	}

	s.StartTime = time.Now()
	return nil
}

//! soft delete
// func (s *Cloud) GetRoutes() map[string]http.HandlerFunc {
// 	return map[string]http.HandlerFunc{
// 		"/api/status":            s.handleStatus,
// 		"/api/files":             s.handleFiles,
// 		"/api/mkdir":             s.handleMkdir,
// 		"/api/delete":            s.handleDelete,
// 		"/strct_agent/fs/upload": s.handleUpload,
// 	}
// }

func (s *Cloud) handleStatus(w http.ResponseWriter, r *http.Request) {
	realFree, _ := disk.GetFreeDiskSpace(s.DataDir)

	userUsed, err := disk.GetDirSize(s.DataDir)
	if err != nil {
		log.Printf("Error calculating dir size: %v", err)
	}

	virtualTotal := userUsed + realFree

	localIP := netx.GetOutboundIP()
	uptime := int64(time.Since(s.StartTime).Seconds())

	resp := StatusResponse{
		IsOnline: true,
		Used:     userUsed,
		Total:    virtualTotal,
		IP:       localIP,
		Uptime:   uptime,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Cloud) handleFiles(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Query().Get("path")
	fullPath, err := secureJoin(s.DataDir, reqPath)
	if err != nil {
		http.Error(w, "Access Denied", http.StatusForbidden)
		return
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		json.NewEncoder(w).Encode(FilesResponse{Files: []FileItem{}})
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(FilesResponse{Files: fileList})
}

func (s *Cloud) handleMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Name == "" || strings.Contains(req.Name, "/") || strings.Contains(req.Name, "\\") {
		http.Error(w, "Invalid folder name", http.StatusBadRequest)
		return
	}

	parentDir, err := secureJoin(s.DataDir, req.Path)
	if err != nil {
		http.Error(w, "Access Denied", http.StatusForbidden)
		return
	}

	newFolderPath := filepath.Join(parentDir, req.Name)

	if err := os.Mkdir(newFolderPath, 0755); err != nil {
		if os.IsExist(err) {
			http.Error(w, "Folder already exists", http.StatusConflict)
			return
		}
		log.Printf("Error creating folder: %v", err)
		http.Error(w, "Could not create folder", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "created"})
}

func (s *Cloud) handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	targetPath := r.URL.Query().Get("path")

	fullPath, err := secureJoin(s.DataDir, targetPath)
	if err != nil {
		http.Error(w, "Access Denied", http.StatusForbidden)
		return
	}

	if fullPath == s.DataDir {
		http.Error(w, "Cannot delete root directory", http.StatusForbidden)
		return
	}

	if err := os.RemoveAll(fullPath); err != nil {
		log.Printf("Error deleting %s: %v", fullPath, err)
		http.Error(w, "Could not delete item", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Deleted"))
}

func (s *Cloud) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	targetDir := r.URL.Query().Get("path")
	saveDir, err := secureJoin(s.DataDir, targetDir)
	if err != nil {
		http.Error(w, "Access Denied", http.StatusForbidden)
		return
	}

	r.ParseMultipartForm(32 << 20)

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Invalid file", 400)
		return
	}
	defer file.Close()

	dstPath := filepath.Join(saveDir, header.Filename)
	dst, err := os.Create(dstPath)
	if err != nil {
		http.Error(w, "Disk error", 500)
		return
	}
	defer dst.Close()

	io.Copy(dst, file)
	w.Write([]byte("Uploaded"))
}

func secureJoin(root, userPath string) (string, error) {
	if userPath == "" {
		userPath = "/"
	}
	clean := filepath.Clean(filepath.Join("/", userPath))
	full := filepath.Join(root, clean)

	if !strings.HasPrefix(full, root) {
		return "", fmt.Errorf("path traversal attempt")
	}
	return full, nil
}
