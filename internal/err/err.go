package err
import (
 "encoding/json"
 "errors"
 "log"
 "net/http"
 "strings"
)

// Kind defines the category of the error.
// This allows the API layer to decide the HTTP Status Code automatically.
type Kind uint8

const (
 KindOther Kind = iota // Unclassified error
 KindIO // Disk, File System issues
 KindNetwork // DNS, Ping, WiFi, Tunnel issues
 KindInvalid // Validation errors (User input)
 KindUnauthorized // Auth token missing/invalid
 KindNotFound // File or Route not found
 KindSystem // OS level failures (exec, mounting)
)

// Op represents the operation where the error occurred (e.g., "cloud.Upload", "wifi.Connect").
type Op string

// Error is our custom error struct.
type Error struct {
 Op Op // Where did it happen?
 Kind Kind // What category is it?
 Err error // The underlying error (the root cause)
 Message string // Human-readable message for the user/frontend
}

// E is a constructor for building errors concisely.
// Usage: errors.E(op, errors.KindNetwork, err, "Connection failed")
func E(args ...interface{}) error {
 e := &Error{}
 for _, arg := range args {
  switch arg := arg.(type) {
  case Op:
   e.Op = arg
  case Kind:
   e.Kind = arg
  case error:
   e.Err = arg
  case string:
   e.Message = arg
  case *Error:
   // Copy the copy
   copy := *arg
   e.Err = &copy
  }
 }
 return e
}

// Error implements the standard error interface.
// It formats the error as: "op: message: underlying_error"
func (e *Error) Error() string {
 var b strings.Builder
	
 // 1. Add Operation
 if e.Op != "" {
  b.WriteString(string(e.Op))
 }

 // 2. Add Message
 if e.Message != "" {
  if b.Len() > 0 {
   b.WriteString(": ")
  }
  b.WriteString(e.Message)
 }

 // 3. Add Underlying Error
 if e.Err != nil {
  if b.Len() > 0 {
   b.WriteString(": ")
  }
  b.WriteString(e.Err.Error())
 }

 return b.String()
}

// Unwrap allows standard errors.Is and errors.As to work.
func (e *Error) Unwrap() error {
 return e.Err
}

// -------------------------------------------------------------------------
// HTTP Helpers (Crucial for your Cloud/Monitor APIs)
// -------------------------------------------------------------------------

// HTTPResponse sends a JSON error response based on the error Kind.
func HTTPResponse(w http.ResponseWriter, err error) {
 // 1. Log the full internal details (Op stack + root cause) to the console
 log.Printf("[API ERROR] %v", err)

 // 2. Determine Status Code and Message
 code := http.StatusInternalServerError
 msg := "Internal Server Error"

 var e *Error
 if errors.As(err, &e) {
  switch e.Kind {
  case KindInvalid:
   code = http.StatusBadRequest
  case KindUnauthorized:
   code = http.StatusUnauthorized
  case KindNotFound:
   code = http.StatusNotFound
  case KindIO, KindSystem:
   code = http.StatusInternalServerError
  }

  // If we set a custom user-facing message, use it.
  // Otherwise, only show the message if it's NOT a 500 (security).
  if e.Message != "" {
   msg = e.Message
  } else if code != http.StatusInternalServerError {
   msg = e.Err.Error()
  }
 }

 w.Header().Set("Content-Type", "application/json")
 w.WriteHeader(code)
 json.NewEncoder(w).Encode(map[string]string{
  "error": msg,
 })
}