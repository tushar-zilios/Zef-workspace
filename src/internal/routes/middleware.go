package routes

import (
	"context"
	"net/http"
	"strings"
	"time"

	"workspace/src/internal/logger"
	"workspace/src/internal/utils"

	"github.com/go-chi/chi/v5/middleware"
)

func handlerLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		logger.LogHandler("%s %s %s from %s - %d %dB in %s",
			r.Method, r.URL.Path, r.Proto, r.RemoteAddr,
			ww.Status(), ww.BytesWritten(), time.Since(start))
	})
}

// JWTMiddleware verifies the shared-secret JWT issued by any Zef service
// (backend/cto/accountant/workspace all sign with the same JWT_SECRET).
func JWTMiddleware(next http.Handler) http.Handler {
	return jwtMiddleware(next, false)
}

// JWTMiddlewareAllowQueryToken additionally accepts the JWT via a `?token=`
// query parameter, needed for EventSource/SSE and WebSocket requests which
// cannot set an Authorization header. Only wire this onto streaming routes —
// a query-param token leaks into access logs, browser history, and Referer.
// requestTimeout bounds how long a request can wait on downstream work (e.g. a
// pgxpool connection acquire) before failing fast. Excludes the messaging SSE
// stream, which is long-lived by design.
func requestTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/messaging/stream" {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func JWTMiddlewareAllowQueryToken(next http.Handler) http.Handler {
	return jwtMiddleware(next, true)
}

func jwtMiddleware(next http.Handler, allowQueryToken bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rawToken string

		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				utils.WriteError(w, http.StatusUnauthorized, "Authorization header must be in format Bearer <token>")
				return
			}
			rawToken = parts[1]
		} else if allowQueryToken {
			rawToken = r.URL.Query().Get("token")
		}

		if rawToken == "" {
			utils.WriteError(w, http.StatusUnauthorized, "Authorization required")
			return
		}

		claims, err := utils.VerifyToken(rawToken)
		if err != nil {
			utils.WriteError(w, http.StatusUnauthorized, "Invalid or expired token")
			return
		}
		ctx := context.WithValue(r.Context(), "user_id", claims.UserID)
		ctx = context.WithValue(ctx, "email", claims.Email)
		ctx = context.WithValue(ctx, "role", claims.Role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func conditionalLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		middleware.Logger(next).ServeHTTP(w, r)
	})
}
