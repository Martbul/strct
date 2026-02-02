package fileserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/strct-org/strct-agent/utils"
)

type Server struct {
	RootPath  string
	StartTime time.Time
}

type StatusResponse struct {
	IsOnline bool   `json:"isOnline"`
	Used     uint64 `json:"used"`   // Bytes
	Total    uint64 `json:"total"`  // Bytes
	IP       string `json:"ip"`
	Uptime   int64  `json:"uptime"` // Seconds
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

func Start(rootPath string, port int, dev bool) {
	absPath, err := filepath.Abs(rootPath)
	if err != nil {
		absPath = filepath.Clean(rootPath)
	}

	if err := os.MkdirAll(absPath, 0755); err != nil {
		log.Printf("[FILESERVER] Error creating root path: %v", err)
	}

	srv := &Server{
		RootPath:  absPath,
		StartTime: time.Now(),
	}

	finalPort := port
	if dev {
		if port <= 1024 {
			log.Printf("[FILESERVER] Dev Mode detected: Switching from privileged port %d to 8080", port)
			finalPort = 8080
		}
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<h1>Strct Agent is Online</h1><p>API endpoints: /api/status, /api/files</p>"))
	})

	mux.HandleFunc("/api/status", srv.handleStatus)

	mux.HandleFunc("/api/files", srv.handleFiles) 
	
	mux.HandleFunc("/strct_agent/fs/upload", srv.handleUpload)

	fileHandler := http.StripPrefix("/files/", http.FileServer(http.Dir(absPath)))
	mux.Handle("/files/", fileHandler)

	log.Printf("[FILESERVER] Starting Native Server on port %d serving %s (Dev: %v)", finalPort, absPath, dev)

	handlerWithCors := corsMiddleware(mux)

	if err := http.ListenAndServe(fmt.Sprintf(":%d", finalPort), handlerWithCors); err != nil {
		log.Printf("[FILESERVER] Error: %v", err)
	}
}


func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	var stat syscall.Statfs_t
	var total, free, used uint64

	if err := syscall.Statfs(s.RootPath, &stat); err == nil {
		total = stat.Blocks * uint64(stat.Bsize)
		free = stat.Bfree * uint64(stat.Bsize)
		used = total - free
	}

	// 2. Get Local IP
	localIP := utils.GetOutboundIP()

	// 3. Calculate Uptime
	uptime := int64(time.Since(s.StartTime).Seconds())

	resp := StatusResponse{
		IsOnline: true,
		Used:     used,
		Total:    total,
		IP:       localIP,
		Uptime:   uptime,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	// Security: Ensure path is valid
	reqPath := r.URL.Query().Get("path")
	fullPath, err := secureJoin(s.RootPath, reqPath)
	if err != nil {
		http.Error(w, "Access Denied", http.StatusForbidden)
		return
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		// If folder doesn't exist, return empty list instead of 404 to prevent React errors
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
			fileType = "folder" // Changed to match React's "folder" (was "dir")
		}

		fileList = append(fileList, FileItem{
			Name:       e.Name(),
			Size:       utils.FormatBytes(info.Size()), // Convert bytes to string (e.g. "1.2 MB") or just stringified int
			Type:       fileType,
			ModifiedAt: info.ModTime().Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(FilesResponse{Files: fileList})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	targetDir := r.URL.Query().Get("path")
	saveDir, err := secureJoin(s.RootPath, targetDir)
	if err != nil {
		http.Error(w, "Access Denied", http.StatusForbidden)
		return
	}

	r.ParseMultipartForm(32 << 20) // 32MB RAM limit

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


func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		allowedOrigins := map[string]bool{
			"https://portal.strct.org":     true,
			"https://dev.portal.strct.org": true,
			"http://localhost:3001":        true,
			"http://localhost:3000":        true, 
		}

		if allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
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
