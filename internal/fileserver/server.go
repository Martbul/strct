//! curently using direct architecture(getting req directly from the portal), the req should come throght the API
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
)

type Server struct {
	RootPath string
}

func Start(rootPath string, port int) {
	// Ensure root directory exists
	if err := os.MkdirAll(rootPath, 0755); err != nil {
		log.Printf("[FILESERVER] Error creating root path: %v", err)
	}

	srv := &Server{RootPath: rootPath}

	mux := http.NewServeMux()

	// 1. API Endpoints (JSON)
	mux.HandleFunc("/api/fs/list", srv.handleList)
	mux.HandleFunc("/api/fs/upload", srv.handleUpload)

	// 2. Raw File Serving
	// Strip "/files/" prefix so url "/files/foo.jpg" -> looks for "foo.jpg" on disk
	fileHandler := http.StripPrefix("/files/", http.FileServer(http.Dir(rootPath)))
	mux.Handle("/files/", fileHandler)

	log.Printf("[FILESERVER] Starting Native Server on port %d serving %s", port, rootPath)

	// 3. Wrap everything in CORS so portal.strct.org can talk to us
	handlerWithCors := corsMiddleware(mux)

	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), handlerWithCors); err != nil {
		log.Printf("[FILESERVER] Error: %v", err)
	}
}


func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		
		allowedOrigins := map[string]bool{
			"https://portal.strct.org":     true,
			"https://dev.portal.strct.org": true,
			"http://localhost:3001":        true, 
		}

		if allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		// If this is a preflight check, return OK immediately
		if r.Method == "OPTIONS" {
			return
		}

		next.ServeHTTP(w, r)
	})
}


func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	// Security: Ensure path is valid
	reqPath := r.URL.Query().Get("path")
	fullPath, err := secureJoin(s.RootPath, reqPath)
	if err != nil {
		http.Error(w, "Access Denied", http.StatusForbidden)
		return
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		http.Error(w, "Directory not found", http.StatusNotFound)
		return
	}

	var response []map[string]interface{}
	for _, e := range entries {
		info, _ := e.Info()
		fileType := "file"
		if e.IsDir() {
			fileType = "dir"
		}

		response = append(response, map[string]interface{}{
			"name": e.Name(),
			"size": info.Size(),
			"type": fileType,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	targetDir := r.URL.Query().Get("path")
	saveDir, err := secureJoin(s.RootPath, targetDir)
	if err != nil {
		http.Error(w, "Access Denied", 403)
		return
	}

	// Limit memory usage for upload parsing (32MB RAM), rest on disk
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

// Helper to prevent Directory Traversal (e.g. "../../../etc/passwd")
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