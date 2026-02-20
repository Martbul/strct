// // Tests for the API server layer: CORS middleware, port selection, shutdown.
// // We test the middleware in isolation — not the features it serves.
package api_test

// import (
// 	"context"
// 	"fmt"
// 	"net/http"
// 	"net/http/httptest"
// 	"testing"
// 	"time"

// 	"github.com/strct-org/strct-agent/internal/api"
// )

// // ---------------------------------------------------------------------------
// // CORS middleware
// // ---------------------------------------------------------------------------

// // newTestServer builds an api.Server wired to a simple echo handler.
// // We use httptest.NewServer to bind a real (random) port so we can test
// // the full server lifecycle without hardcoding ports.
// func newTestMux() *http.ServeMux {
// 	mux := http.NewServeMux()
// 	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
// 		w.WriteHeader(http.StatusOK)
// 		w.Write([]byte("pong"))
// 	})
// 	return mux
// }

// func TestCORSMiddleware_AllowedOrigins(t *testing.T) {
// 	tests := []struct {
// 		origin      string
// 		wantAllowed bool
// 	}{
// 		{"http://localhost:3000", true},
// 		{"http://localhost:5173", true},
// 		{"https://app.strct.org", true},
// 		{"https://strct.org", true},
// 		{"https://evil.com", false},
// 		{"", false},
// 	}

// 	// We test CORS by calling the server's handler directly via httptest.
// 	// api.New is not started — we just use it to get the wrapped handler.
// 	srv := api.New(api.Config{Port: 8080, IsDev: false}, newTestMux())
// 	handler := srv.Handler() // see note: add Handler() method to api.Server

// 	for _, tt := range tests {
// 		t.Run(tt.origin, func(t *testing.T) {
// 			req := httptest.NewRequest(http.MethodGet, "/ping", nil)
// 			req.Header.Set("Origin", tt.origin)
// 			w := httptest.NewRecorder()
// 			handler.ServeHTTP(w, req)

// 			got := w.Header().Get("Access-Control-Allow-Origin")
// 			if tt.wantAllowed && got != tt.origin {
// 				t.Errorf("origin %q: expected ACAO=%q, got %q", tt.origin, tt.origin, got)
// 			}
// 			if !tt.wantAllowed && got != "" {
// 				t.Errorf("origin %q: expected no ACAO header, got %q", tt.origin, got)
// 			}
// 		})
// 	}
// }

// func TestCORSMiddleware_PreflightOptions(t *testing.T) {
// 	srv := api.New(api.Config{Port: 8080, IsDev: false}, newTestMux())
// 	handler := srv.Handler()

// 	req := httptest.NewRequest(http.MethodOptions, "/ping", nil)
// 	req.Header.Set("Origin", "http://localhost:3000")
// 	req.Header.Set("Access-Control-Request-Method", "POST")
// 	w := httptest.NewRecorder()
// 	handler.ServeHTTP(w, req)

// 	if w.Code != http.StatusOK {
// 		t.Errorf("preflight: expected 200, got %d", w.Code)
// 	}
// 	if w.Header().Get("Access-Control-Allow-Methods") == "" {
// 		t.Error("preflight: missing Access-Control-Allow-Methods header")
// 	}
// }

// // ---------------------------------------------------------------------------
// // Port selection (dev mode redirect)
// // ---------------------------------------------------------------------------

// // Note: we can't easily test the port selection in Start() without binding
// // a real port. The correct approach is to extract port selection into a
// // pure function:
// //
// //   func effectivePort(cfg Config) int {
// //       if cfg.IsDev && cfg.Port <= 1024 { return 8080 }
// //       return cfg.Port
// //   }
// //
// // Then test that directly without any server lifecycle. This is a common
// // Go pattern: isolate the decision from the side effect.

// // ---------------------------------------------------------------------------
// // Graceful shutdown
// // ---------------------------------------------------------------------------

// func TestServer_GracefulShutdown(t *testing.T) {
// 	// Use a random available port to avoid conflicts.
// 	mux := http.NewServeMux()
// 	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
// 		// Simulate a handler that takes some time.
// 		time.Sleep(50 * time.Millisecond)
// 		w.WriteHeader(http.StatusOK)
// 	})

// 	srv := api.New(api.Config{Port: 0, IsDev: true}, mux)
// 	// Port 0 = OS assigns a random free port.
// 	// api.Server needs to expose the actual bound address for this test to work:
// 	//   func (s *Server) Addr() string — returns the net.Listener address after Start.

// 	ctx, cancel := context.WithCancel(context.Background())

// 	done := make(chan error, 1)
// 	go func() {
// 		done <- srv.Start(ctx)
// 	}()

// 	// Give the server time to bind.
// 	time.Sleep(50 * time.Millisecond)

// 	addr := srv.Addr() // ":<port>"
// 	if addr == "" {
// 		t.Skip("server did not expose Addr() — add it to api.Server")
// 	}

// 	// Kick off a request that will be in-flight during shutdown.
// 	go http.Get(fmt.Sprintf("http://localhost%s/slow", addr))
// 	time.Sleep(10 * time.Millisecond)

// 	// Signal shutdown.
// 	cancel()

// 	select {
// 	case err := <-done:
// 		if err != nil {
// 			t.Errorf("Start() returned error after shutdown: %v", err)
// 		}
// 	case <-time.After(3 * time.Second):
// 		t.Error("server did not shut down within 3 seconds")
// 	}
// }
