package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
)

type APIResponse struct {
	Success   bool        `json:"success"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	ErrorCode string      `json:"errorCode,omitempty"`
	Timestamp string      `json:"timestamp,omitempty"`
}

func JSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func OK(w http.ResponseWriter, message string, data interface{}) {
	JSON(w, http.StatusOK, APIResponse{Success: true, Message: message, Data: data})
}

func Created(w http.ResponseWriter, message string, data interface{}) {
	JSON(w, http.StatusCreated, APIResponse{Success: true, Message: message, Data: data})
}

func Fail(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, APIResponse{
		Success: false, Message: message, ErrorCode: code,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func DecodeJSON(r *http.Request, dst interface{}) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("JSON inválido: %w", err)
	}
	return nil
}

type Middleware func(http.Handler) http.Handler

func Chain(handler http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

func CORS(allowedOrigin string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := allowedOrigin
			if origin == "*" {
				origin = r.Header.Get("Origin")
				if origin == "" {
					origin = "*"
				}
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID, X-Service-Key")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type contextKey string

const requestIDKey contextKey = "requestID"

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", requestID)
		r.Header.Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func Logging(service string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, r)
			log.Printf("service=%s requestId=%s method=%s path=%s status=%d durationMs=%d",
				service, RequestIDFromContext(r.Context()), r.Method, r.URL.Path, recorder.status, time.Since(started).Milliseconds())
		})
	}
}

func Recover(service string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Printf("service=%s panic=%v stack=%s", service, recovered, debug.Stack())
					Fail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Ocurrió un error interno")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func RequireServiceKey(expected string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if expected == "" || r.Header.Get("X-Service-Key") != expected {
				Fail(w, http.StatusUnauthorized, "INVALID_SERVICE_KEY", "Acceso interno no autorizado")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func HealthHandler(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		OK(w, "Servicio disponible", map[string]interface{}{
			"service": service,
			"status":  "UP",
			"time":    time.Now().UTC().Format(time.RFC3339),
		})
	}
}

func MethodNotAllowed(w http.ResponseWriter) {
	Fail(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Método HTTP no permitido")
}

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

var ErrNotFound = errors.New("not found")
