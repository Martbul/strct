package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/strct-org/strct-agent/internal/errs"
)

const opStart errs.Op = "api.Server.Start"

// Config holds the server configuration.
type Config struct {
	DataDir string
	Port    int
	IsDev   bool
}

// Server is a runnable HTTP server.
// It accepts a pre-built mux so route registration stays in main.
type Server struct {
	cfg Config
	mux *http.ServeMux
}

// New returns a Server ready to Start.
func New(cfg Config, mux *http.ServeMux) *Server {
	return &Server{cfg: cfg, mux: mux}
}

// Start implements agent.Service.
func (s *Server) Start(ctx context.Context) error {
	port := s.cfg.Port
	if s.cfg.IsDev && port <= 1024 {
		log.Printf("[API] Dev mode: redirecting port %d → 8080", port)
		port = 8080
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: corsMiddleware(s.mux),
	}

	go func() {
		<-ctx.Done()
		log.Println("[API] Shutting down...")
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()

	log.Printf("[API] Listening on :%d (dev=%v)", port, s.cfg.IsDev)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return errs.E(opStart, errs.KindNetwork, err, fmt.Sprintf("server failed on port %d", port))
	}
	return nil
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if strings.HasPrefix(origin, "http://localhost") ||
			strings.HasSuffix(origin, ".strct.org") ||
			origin == "https://strct.org" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, Range")
			w.Header().Set("Access-Control-Max-Age", "3600")
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}