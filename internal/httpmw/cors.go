package httpmw

import (
	"net/http"
	"strings"
)

// CORSConfig controls Cross-Origin Resource Sharing behavior.
//
// Zapfast's HTTP API is consumed both server-side (no CORS needed) and from
// browser dashboards (e.g. https://renatokeys.github.io/zapfast/), which
// hit the API from a different origin. Without CORS the browser blocks the
// request before it reaches the server.
type CORSConfig struct {
	// AllowedOrigins is matched against the request's Origin header. Use "*"
	// for any origin (then AllowCredentials must be false per the CORS spec).
	AllowedOrigins []string
	// AllowedMethods sent in Access-Control-Allow-Methods.
	AllowedMethods []string
	// AllowedHeaders sent in Access-Control-Allow-Headers.
	AllowedHeaders []string
	// AllowCredentials controls Access-Control-Allow-Credentials.
	AllowCredentials bool
	// MaxAge in seconds for Access-Control-Max-Age.
	MaxAge int
}

// DefaultCORS returns a config suitable for the public zapfast deployment:
// allows the GitHub Pages dashboard and the standard set of methods/headers
// the API exposes.
func DefaultCORS() CORSConfig {
	return CORSConfig{
		AllowedOrigins: []string{
			"https://renatokeys.github.io",
			"http://localhost:3000",
			"http://localhost:5173",
			"http://127.0.0.1:3000",
		},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
		AllowedHeaders: []string{
			"Authorization",
			"Content-Type",
			"Accept",
			"Origin",
			"X-Requested-With",
			"token",
			"Client-Token",
			"X-Hub-Signature-256",
			"Idempotency-Key",
		},
		AllowCredentials: true,
		MaxAge:           600,
	}
}

// CORS returns middleware applying the given config. Preflight (OPTIONS)
// requests get a 204 with all CORS headers and short-circuit before they
// reach downstream handlers.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	allowedOrigins := make(map[string]struct{}, len(cfg.AllowedOrigins))
	allowAny := false
	for _, o := range cfg.AllowedOrigins {
		if o == "*" {
			allowAny = true
		}
		allowedOrigins[o] = struct{}{}
	}
	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")
	maxAge := ""
	if cfg.MaxAge > 0 {
		maxAge = strIntoa(cfg.MaxAge)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if allowAny {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else if _, ok := allowedOrigins[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
				}
				if cfg.AllowCredentials && !allowAny {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}

			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", methods)
				w.Header().Set("Access-Control-Allow-Headers", headers)
				if maxAge != "" {
					w.Header().Set("Access-Control-Max-Age", maxAge)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func strIntoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
