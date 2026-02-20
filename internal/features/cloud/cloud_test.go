// Blackbox HTTP handler tests for the cloud feature.
// We use httptest.NewRecorder and httptest.NewServer — never a real port.
// No executil.Mock needed here: cloud only does file I/O, not exec calls.
package cloud_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/strct-org/strct-agent/internal/features/cloud"
)

// newTestCloud returns a Cloud pointed at a temp directory.
// It skips SSD detection (IsDev = true) so tests work everywhere.
func newTestCloud(t *testing.T) *cloud.Cloud {
	t.Helper()
	tmp := t.TempDir()
	c, err := cloud.NewFromConfig_Test(tmp) // see note below
	if err != nil {
		t.Fatalf("could not create test cloud: %v", err)
	}
	return c
}

// Note: NewFromConfig_Test is a test-only constructor that accepts a dataDir
// directly instead of reading from *config.Config. Add this to cloud.go:
//
//   func NewFromConfig_Test(dataDir string) (*Cloud, error) {
//       c := New(dataDir, 8080, true) // isDev=true skips SSD detection
//       if err := c.initFileSystem(); err != nil {
//           return nil, err
//       }
//       return c, nil
//   }
//
// This is the idiomatic Go approach: a package-level test helper constructor,
// not a separate mock. It lives in cloud.go (not cloud_test.go) because
// tests in external packages (cloud_test) need to import it.

// buildMux wires up the cloud routes on a fresh mux.
func buildMux(t *testing.T, c *cloud.Cloud) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	c.RegisterRoutes(mux)
	return mux
}

// ---------------------------------------------------------------------------
// GET /api/status
// ---------------------------------------------------------------------------

func TestHandleStatus_Returns200WithExpectedShape(t *testing.T) {
	c := newTestCloud(t)
	mux := buildMux(t, c)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		IsOnline bool   `json:"isOnline"`
		IP       string `json:"ip"`
		Uptime   int64  `json:"uptime"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}
	if !resp.IsOnline {
		t.Error("expected isOnline to be true")
	}
	if resp.IP == "" {
		t.Error("expected non-empty IP")
	}
}

// ---------------------------------------------------------------------------
// GET /api/files
// ---------------------------------------------------------------------------

func TestHandleFiles_EmptyDir_ReturnsEmptyList(t *testing.T) {
	c := newTestCloud(t)
	mux := buildMux(t, c)

	req := httptest.NewRequest(http.MethodGet, "/api/files", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Files []any `json:"files"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Files) != 0 {
		t.Errorf("expected 0 files, got %d", len(resp.Files))
	}
}

func TestHandleFiles_WithFiles_ReturnsThem(t *testing.T) {
	c := newTestCloud(t)

	// Write test files directly into DataDir.
	os.WriteFile(filepath.Join(c.DataDir, "report.pdf"), []byte("pdf content"), 0644)
	os.WriteFile(filepath.Join(c.DataDir, "notes.txt"), []byte("hello"), 0644)
	os.Mkdir(filepath.Join(c.DataDir, "photos"), 0755)

	mux := buildMux(t, c)
	req := httptest.NewRequest(http.MethodGet, "/api/files", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp struct {
		Files []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"files"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Files) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(resp.Files))
	}

	types := map[string]string{}
	for _, f := range resp.Files {
		types[f.Name] = f.Type
	}
	if types["photos"] != "folder" {
		t.Errorf("expected photos to be folder, got %q", types["photos"])
	}
	if types["report.pdf"] != "file" {
		t.Errorf("expected report.pdf to be file, got %q", types["report.pdf"])
	}
}

func TestHandleFiles_PathTraversal_Returns403(t *testing.T) {
	c := newTestCloud(t)
	mux := buildMux(t, c)

	req := httptest.NewRequest(http.MethodGet, "/api/files?path=../../etc", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for traversal, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /api/mkdir
// ---------------------------------------------------------------------------

func TestHandleMkdir_CreatesFolder(t *testing.T) {
	c := newTestCloud(t)
	mux := buildMux(t, c)

	body := `{"path": "/", "name": "documents"}`
	req := httptest.NewRequest(http.MethodPost, "/api/mkdir", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	if _, err := os.Stat(filepath.Join(c.DataDir, "documents")); os.IsNotExist(err) {
		t.Error("folder was not created on disk")
	}
}

func TestHandleMkdir_InvalidName_Returns400(t *testing.T) {
	c := newTestCloud(t)
	mux := buildMux(t, c)

	for _, name := range []string{"", "a/b", `a\b`} {
		body, _ := json.Marshal(map[string]string{"path": "/", "name": name})
		req := httptest.NewRequest(http.MethodPost, "/api/mkdir", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("name %q: expected 400, got %d", name, w.Code)
		}
	}
}

func TestHandleMkdir_Duplicate_Returns409(t *testing.T) {
	c := newTestCloud(t)
	os.Mkdir(filepath.Join(c.DataDir, "existing"), 0755)
	mux := buildMux(t, c)

	body := `{"path": "/", "name": "existing"}`
	req := httptest.NewRequest(http.MethodPost, "/api/mkdir", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// DELETE /api/delete
// ---------------------------------------------------------------------------

func TestHandleDelete_RemovesFile(t *testing.T) {
	c := newTestCloud(t)
	target := filepath.Join(c.DataDir, "deleteme.txt")
	os.WriteFile(target, []byte("bye"), 0644)

	mux := buildMux(t, c)
	req := httptest.NewRequest(http.MethodDelete, "/api/delete?path=/deleteme.txt", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("file still exists after delete")
	}
}

func TestHandleDelete_RootDir_Returns403(t *testing.T) {
	c := newTestCloud(t)
	mux := buildMux(t, c)

	req := httptest.NewRequest(http.MethodDelete, "/api/delete?path=/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for root delete, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /strct_agent/fs/upload
// ---------------------------------------------------------------------------

func TestHandleUpload_StoresFile(t *testing.T) {
	c := newTestCloud(t)
	mux := buildMux(t, c)

	// Build a multipart form body manually.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(fw, "hello world")
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/strct_agent/fs/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	content, err := os.ReadFile(filepath.Join(c.DataDir, "hello.txt"))
	if err != nil {
		t.Fatalf("uploaded file not found on disk: %v", err)
	}
	if string(content) != "hello world" {
		t.Errorf("file content = %q, want %q", content, "hello world")
	}
}
