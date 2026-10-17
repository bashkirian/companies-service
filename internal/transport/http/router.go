package http

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/bashkirian/companies-service/internal/adapter/jwt"
	"github.com/bashkirian/companies-service/internal/domain"
	"github.com/bashkirian/companies-service/internal/observability"
)

type CompanyService interface {
	Create(ctx context.Context, p domain.CreateParams) (domain.Company, error)
	Get(ctx context.Context, id uuid.UUID) (domain.Company, error)
	Patch(ctx context.Context, id uuid.UUID, expectedVersion int64, p domain.PatchParams) (domain.Company, error)
	Delete(ctx context.Context, id uuid.UUID, expectedVersion int64) error
}

type TokenValidator interface {
	Validate(raw string) (jwt.Claims, error)
}

// ReadinessCheck reports whether a dependency is usable.
type ReadinessCheck func(ctx context.Context) error

type Deps struct {
	Log       *zap.Logger
	Service   CompanyService
	Validator TokenValidator
	Metrics   *observability.Metrics
	Ready     map[string]ReadinessCheck
}

func NewRouter(d Deps) http.Handler {
	h := &companyHandler{svc: d.Service, log: d.Log}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(accessLog(d.Log))
	r.Use(httpMetrics(d.Metrics))
	// recoverer runs inside accessLog and httpMetrics, so panics are still logged and counted as 500.
	r.Use(recoverer(d.Log))

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/ready", readiness(d.Ready, d.Log))

	r.Route("/api/v1/companies", func(r chi.Router) {
		r.Get("/{id}", h.get)

		r.Group(func(r chi.Router) {
			r.Use(authenticate(d.Validator))
			r.Post("/", h.create)
			r.Patch("/{id}", h.patch)
			r.Delete("/{id}", h.delete)
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusNotFound, "route not found", nil)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
	})
	return r
}

// NewAdminRouter serves operational endpoints that must not be exposed with the public API.
func NewAdminRouter(m *observability.Metrics) http.Handler {
	r := chi.NewRouter()
	r.Handle("/metrics", promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{}))
	return r
}

func readiness(checks map[string]ReadinessCheck, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		result := make(map[string]string, len(checks))
		for name, check := range checks {
			if err := check(r.Context()); err != nil {
				status = http.StatusServiceUnavailable
				log.Warn("readiness check failed", zap.String("check", name), zap.Error(err))
				result[name] = "fail"
				continue
			}
			result[name] = "ok"
		}
		writeJSON(w, status, result)
	}
}
