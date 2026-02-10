package api

import (
	"fmt"
	"log"
	"net/http"

	"github.com/strct-org/strct-agent/internal/errs"
)

const OpStart errs.Op = "api.Start"

type Config struct {
	DataDir string
	Port    int
	IsDev   bool
}

func Start(cfg Config, routes map[string]http.HandlerFunc) error {

	finalPort := cfg.Port
	if cfg.IsDev {
		if cfg.Port <= 1024 {
			log.Printf("[API] Dev Mode detected: Switching from privileged port %d to 8080", cfg.Port)
			finalPort = 8080
		}
	}

	mux := http.NewServeMux()

	for path, handler := range routes {
		mux.HandleFunc(path, handler)
	}

	if cfg.DataDir != "" {
		fileHandler := http.StripPrefix("/files/", http.FileServer(http.Dir(cfg.DataDir)))
		mux.Handle("/files/", fileHandler)
	}

	addr := fmt.Sprintf(":%d", finalPort)
	log.Printf("[API] Starting Native Server on port %d serving %s (Dev: %v)", finalPort, cfg.DataDir, cfg.IsDev)

	handlerWithCors := corsMiddleware(mux)

	if err := http.ListenAndServe(addr, handlerWithCors); err != nil {
		return errs.E(OpStart, errs.KindNetwork, err, fmt.Sprintf("server failed on port %d", finalPort))
	}

	return nil
}
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		allowedOrigins := map[string]bool{
			"https://strct.org":         true,
			"https://dev.strct.org":     true,
			"https://api.strct.org":     true,
			"https://dev.api.strct.org": true,
			"http://localhost:3001":     true,
			"http://localhost:3000":     true,
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
