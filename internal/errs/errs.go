package errs
import (
 "encoding/json"
 "errors"
 "log"
 "net/http"
 "strings"
)

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

type Op string

type Error struct {
 Op Op // Where did it happen?
 Kind Kind // What category is it?
 Err error // The underlying error 
 Message string // Human-readable message for the user/frontend
}

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
   copy := *arg
   e.Err = &copy
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
 log.Printf("[API ERROR] %v", err)

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