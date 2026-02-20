package errs

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

type Kind uint8

const (
	KindOther        Kind = iota // Unclassified — maps to 500
	KindIO                       // Disk / filesystem issues — 500
	KindNetwork                  // DNS, ping, WiFi, tunnel — 503
	KindInvalid                  // Validation / bad user input — 400
	KindUnauthorized             // Auth token missing or invalid — 401
	KindNotFound                 // File or route not found — 404
	KindSystem                   // OS-level failures (exec, mount) — 500
)

type Op string

type Error struct {
	Op      Op     // Where did it happen?
	Kind    Kind   // What category?
	Err     error  // Underlying cause (may be another *Error, wraps correctly)
	Message string // Safe to show to the user / frontend
}

func E(args ...any) error {
	e := &Error{}
	for _, arg := range args {
		switch v := arg.(type) {
		case Op:
			e.Op = v
		case Kind:
			e.Kind = v
		case error:
			e.Err = v
		case string:
			e.Message = v
		case *Error:
			cp := *v
			e.Err = &cp
		}
	}
	return e
}

func (e *Error) Error() string {
	var b strings.Builder
	if e.Op != "" {
		b.WriteString(string(e.Op))
	}
	if e.Message != "" {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(e.Message)
	}
	if e.Err != nil {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

func (e *Error) Unwrap() error {
	return e.Err
}

func HTTPResponse(w http.ResponseWriter, err error) {
	slog.Error("errs: request failed", "err", err)

	code := http.StatusInternalServerError
	msg := "internal server error"

	var e *Error
	if errors.As(err, &e) {
		code = kindToStatus(e.Kind)

		if e.Message != "" {
			msg = e.Message
		} else if code != http.StatusInternalServerError && e.Err != nil {
			msg = e.Err.Error()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func kindToStatus(k Kind) int {
	switch k {
	case KindInvalid:
		return http.StatusBadRequest // 400
	case KindUnauthorized:
		return http.StatusUnauthorized // 401
	case KindNotFound:
		return http.StatusNotFound // 404
	case KindNetwork:
		return http.StatusServiceUnavailable // 503
	case KindIO, KindSystem, KindOther:
		return http.StatusInternalServerError // 500
	default:
		return http.StatusInternalServerError
	}
}
