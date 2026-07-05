package utils

import (
	"net/http"
	"os"
	"strings"
)

// allowedOrigins returns the set of origins permitted to make credentialed
// cross-origin requests, read from CORS_ALLOWED_ORIGINS (comma-separated),
// defaulting to the local Vite dev server. ngrok tunnel domains are always
// allowed since the frontend's fetch interceptor is built to work through them.
func allowedOrigins() []string {
	origins := []string{"http://localhost:5173"}
	if v := os.Getenv("CORS_ALLOWED_ORIGINS"); v != "" {
		for _, o := range strings.Split(v, ",") {
			if o = strings.TrimSpace(o); o != "" {
				origins = append(origins, o)
			}
		}
	}
	return origins
}

func isOriginAllowed(origin string) bool {
	if origin == "" {
		return false
	}
	if strings.HasSuffix(origin, ".ngrok-free.app") || strings.HasSuffix(origin, ".ngrok.io") || strings.HasSuffix(origin, ".ngrok.app") {
		return true
	}
	for _, o := range allowedOrigins() {
		if o == origin {
			return true
		}
	}
	return false
}

// CORSMiddleware handles Cross-Origin Resource Sharing (CORS) headers.
// It reflects back the request Origin only if it is on the allowlist
// (CORS_ALLOWED_ORIGINS env var, plus localhost dev and ngrok tunnels),
// standard HTTP methods, and common headers like Content-Type and Authorization.
// It also responds immediately to preflight OPTIONS requests with a 200 OK status.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if isOriginAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Timing-Allow-Origin", origin)
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, ngrok-skip-browser-warning")
		w.Header().Set("Access-Control-Max-Age", "86400")

		// If it's a preflight request, respond immediately with 200 OK
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
