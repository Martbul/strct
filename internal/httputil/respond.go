// Intentionally thin — just enough to enforce consistent Content-Type, status codes,
package httputil

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// JSON writes a JSON-encoded payload with the given HTTP status code.
// If encoding fails, it writes a plain 500 error instead.
func JSON(w http.ResponseWriter, code int, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		slog.Error("httputil: failed to marshal JSON response", "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(b)
}

// Error writes a JSON error body: {"error": "<message>"} with the given status code.
func Error(w http.ResponseWriter, code int, message string) {
	JSON(w, code, map[string]string{"error": message})
}

// OK writes a 200 JSON response. Convenience wrapper for the common case.
func OK(w http.ResponseWriter, payload any) {
	JSON(w, http.StatusOK, payload)
}

// NoContent writes 204 with no body. Use for DELETE and actions with no return value.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// BadRequest writes 400 with a message. Use for invalid input.
func BadRequest(w http.ResponseWriter, message string) {
	Error(w, http.StatusBadRequest, message)
}

// InternalError writes 500. Use when something unexpected went wrong server-side.
func InternalError(w http.ResponseWriter, message string) {
	Error(w, http.StatusInternalServerError, message)
}

// Forbidden writes 403. Use for path traversal attempts and access control violations.
func Forbidden(w http.ResponseWriter) {
	Error(w, http.StatusForbidden, "access denied")
}

// MethodNotAllowed writes 405. Use only on handlers that haven't migrated to method-specific routing yet.
func MethodNotAllowed(w http.ResponseWriter) {
	Error(w, http.StatusMethodNotAllowed, "method not allowed")
}