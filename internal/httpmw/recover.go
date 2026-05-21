// Package httpmw collects net/http middleware that's shared across the API.
//
// The first member is panic recovery. Wuzapi's legacy handlers run with no
// recovery — a single nil pointer dereference (e.g. accessing a *whatsmeow.Client
// for a user that never connected) takes down the gorilla mux handler and
// leaks the goroutine with a 5xx. The recovery middleware turns that into
// a structured 500 response, logs the stack trace, and keeps the worker pool
// healthy.
package httpmw

import (
	"encoding/json"
	"net/http"
	"runtime/debug"

	"github.com/rs/zerolog"
)

// Recover returns middleware that catches panics from the wrapped handler,
// emits a zerolog error event with the stack trace, and writes a JSON 500
// to the client. The logger argument lets callers route to the global
// router log or to a request-scoped logger from hlog.
func Recover(logger zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				stack := debug.Stack()
				logger.Error().
					Interface("panic", rec).
					Bytes("stack", stack).
					Str("method", r.Method).
					Str("url", r.URL.String()).
					Msg("panic recovered in HTTP handler")

				// If the handler already wrote headers we cannot change them.
				// rw is the original ResponseWriter, so this is best-effort.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":    500,
					"success": false,
					"error":   "internal server error",
				})
			}()
			next.ServeHTTP(w, r)
		})
	}
}
