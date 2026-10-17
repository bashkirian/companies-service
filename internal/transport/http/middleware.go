package http

import (
	"context"
	"errors"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/bashkirian/companies-service/internal/observability"
)

// subjectHolderKey carries a pointer that authenticate fills, so accessLog can log the caller.
type subjectHolderKey struct{}

func authenticate(v TokenValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scheme, raw, _ := strings.Cut(r.Header.Get("Authorization"), " ")
			raw = strings.TrimSpace(raw)
			if !strings.EqualFold(scheme, "Bearer") || raw == "" { // the scheme is case-insensitive (RFC 9110)
				w.Header().Set("WWW-Authenticate", `Bearer`)
				writeProblem(w, r, http.StatusUnauthorized, "missing bearer token", nil)
				return
			}
			claims, err := v.Validate(raw)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				writeProblem(w, r, http.StatusUnauthorized, "invalid token", nil)
				return
			}
			if holder, ok := r.Context().Value(subjectHolderKey{}).(*string); ok {
				*holder = claims.Subject
			}
			next.ServeHTTP(w, r)
		})
	}
}

func accessLog(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			var subject string
			next.ServeHTTP(ww, r.WithContext(context.WithValue(r.Context(), subjectHolderKey{}, &subject)))

			if r.URL.Path == "/health" || r.URL.Path == "/ready" {
				return
			}
			log.Info("http request",
				zap.String("request_id", middleware.GetReqID(r.Context())),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.Status()),
				zap.Int("bytes", ww.BytesWritten()),
				zap.Duration("duration", time.Since(start)),
				zap.String("subject", subject),
			)
		})
	}
}

func httpMetrics(m *observability.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			route := chi.RouteContext(r.Context()).RoutePattern()
			if route == "" {
				route = "unmatched"
			}
			m.HTTPRequests.WithLabelValues(r.Method, route, strconv.Itoa(ww.Status())).Inc()
			m.HTTPDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
		})
	}
}

func recoverer(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
						panic(p)
					}
					log.Error("panic recovered",
						zap.String("request_id", middleware.GetReqID(r.Context())),
						zap.Any("panic", p),
						zap.ByteString("stack", debug.Stack()),
					)
					writeProblem(w, r, http.StatusInternalServerError, "internal error", nil)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
