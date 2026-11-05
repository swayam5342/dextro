package httpx

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

type requestIDKey struct{}

// RequestID returns the ID assigned to the current request.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// assignRequestID gives each request a fresh ID. An incoming X-Request-ID is
// ignored so a client can't forge or collide with entries in the logs.
func assignRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := rand.Text()
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// accessLog records one line per request. It logs the path but never the query
// string, because query strings can carry tokens.
func accessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				status := ww.Status()
				if status == 0 {
					status = http.StatusOK
				}
				logger.LogAttrs(r.Context(), slog.LevelInfo, "request",
					slog.String("request_id", RequestID(r.Context())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", status),
					slog.Int("bytes", ww.BytesWritten()),
					slog.Duration("duration", time.Since(start)),
				)
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

// recoverPanics turns a handler panic into a logged error and a generic 500.
func recoverPanics(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				logger.ErrorContext(r.Context(), "handler panicked",
					"request_id", RequestID(r.Context()),
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				WriteError(w, r, http.StatusInternalServerError, CodeInternal, "Something went wrong.")
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// contentSecurityPolicy locks down API responses, which never need to load
// anything. The policy for the UI lives in web/server. Routes serving user
// files must replace this with the policy from the security rules.
const contentSecurityPolicy = "default-src 'none'; frame-ancestors 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
