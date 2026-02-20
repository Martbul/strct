// ? HTTP server plumbing (cors, server)
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/strct-org/strct-agent/internal/errs"
)

const opStart errs.Op = "api.Server.Start"

type Config struct {
	DataDir string
	Port    int
	IsDev   bool
}

type Server struct {
	cfg Config
	mux *http.ServeMux
}

func New(cfg Config, mux *http.ServeMux) *Server {
	return &Server{cfg: cfg, mux: mux}
}

func (s *Server) Start(ctx context.Context) error {
	port := s.cfg.Port
	if s.cfg.IsDev && port <= 1024 {
		slog.Info("api: Dev mode: redirecting API port", "from", port, "to", 8080)
		port = 8080
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: corsMiddleware(s.mux),
	}

	go func() {
		<-ctx.Done()
		slog.Info("api: shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()

	slog.Info("api: starting server", "port", port, "isDev", s.cfg.IsDev)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
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
