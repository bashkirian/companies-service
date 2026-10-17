package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/bashkirian/companies-service/internal/adapter/jwt"
	"github.com/bashkirian/companies-service/internal/domain"
	"github.com/bashkirian/companies-service/internal/observability"
)

const testSecret = "test-secret-test-secret-test-secret"

type fakeService struct {
	company domain.Company
	err     error

	gotVersion int64
	gotPatch   domain.PatchParams
}

func (f *fakeService) Create(_ context.Context, p domain.CreateParams) (domain.Company, error) {
	if f.err != nil {
		return domain.Company{}, f.err
	}
	return domain.NewCompany(p, time.Now())
}

func (f *fakeService) Get(context.Context, uuid.UUID) (domain.Company, error) {
	return f.company, f.err
}

func (f *fakeService) Patch(_ context.Context, _ uuid.UUID, v int64, p domain.PatchParams) (domain.Company, error) {
	f.gotVersion, f.gotPatch = v, p
	return f.company, f.err
}

func (f *fakeService) Delete(_ context.Context, _ uuid.UUID, v int64) error {
	f.gotVersion = v
	return f.err
}

func newTestRouter(t *testing.T, svc CompanyService) http.Handler {
	t.Helper()
	v, err := jwt.NewValidator(jwt.Config{Algorithm: "HS256", Secret: testSecret, Issuer: "test", Audience: "companies"})
	require.NoError(t, err)
	return NewRouter(Deps{Log: zap.NewNop(), Service: svc, Validator: v, Metrics: observability.NewMetrics()})
}

func token(t *testing.T, secret string, exp time.Time) string {
	t.Helper()
	s, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, gojwt.RegisteredClaims{
		Subject: "user-1", Issuer: "test", Audience: gojwt.ClaimStrings{"companies"}, ExpiresAt: gojwt.NewNumericDate(exp),
	}).SignedString([]byte(secret))
	require.NoError(t, err)
	return s
}

func do(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthRequiredForMutations(t *testing.T) {
	h := newTestRouter(t, &fakeService{})
	path := "/api/v1/companies/" + uuid.NewString()

	tests := []struct {
		name   string
		header string
	}{
		{name: "no header"},
		{name: "wrong scheme", header: "Basic abc"},
		{name: "bad signature", header: "Bearer " + token(t, "other-secret-other-secret-other-secret", time.Now().Add(time.Hour))},
		{name: "expired", header: "Bearer " + token(t, testSecret, time.Now().Add(-time.Hour))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
				target := path
				if method == http.MethodPost {
					target = "/api/v1/companies/"
				}
				rec := do(h, method, target, `{}`, map[string]string{"Authorization": tt.header, "If-Match": `"1"`})
				assert.Equal(t, http.StatusUnauthorized, rec.Code, method)
				assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Bearer")
			}
		})
	}
}

func TestGetIsPublicAndSetsETag(t *testing.T) {
	c, err := domain.NewCompany(domain.CreateParams{Name: "Acme", Type: domain.CompanyTypeNonProfit}, time.Now())
	require.NoError(t, err)
	h := newTestRouter(t, &fakeService{company: c})

	rec := do(h, http.MethodGet, "/api/v1/companies/"+c.ID.String(), "", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `"1"`, rec.Header().Get("ETag"))

	var body companyResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, c.ID, body.ID)
	assert.Equal(t, "NonProfit", body.Type)
}

func TestCreate(t *testing.T) {
	h := newTestRouter(t, &fakeService{})
	auth := map[string]string{"Authorization": "Bearer " + token(t, testSecret, time.Now().Add(time.Hour))}

	rec := do(h, http.MethodPost, "/api/v1/companies",
		`{"name":"Acme","amount_of_employees":5,"registered":true,"type":"Sole Proprietorship"}`, auth)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, `"1"`, rec.Header().Get("ETag"))
	assert.True(t, strings.HasPrefix(rec.Header().Get("Location"), "/api/v1/companies/"))

	rec = do(h, http.MethodPost, "/api/v1/companies/", `{"name":"Acme"}`, auth)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var p problem
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&p))
	assert.Len(t, p.Errors, 3)

	valid := `{"name":"Acme","amount_of_employees":5,"registered":true,"type":"NonProfit"}`
	badBodies := map[string]string{
		`{"name":"Acme","id":"x"}`:                                   "id is read-only",
		`{"name":"Acme","NAME":"x"}`:                                 "NAME is unknown",
		`{"name":"Acme","amount_of_employees":1.5}`:                  "amount_of_employees has invalid type",
		`{"name":"Acme","amount_of_employees":30000000000000000000}`: "amount_of_employees has invalid type",
		valid + `{"x":1}`:                                            "",
		valid + ` garbage`:                                           "",
		`not json`:                                                   "",
		`null`:                                                       "",
	}
	for body, want := range badBodies {
		rec = do(h, http.MethodPost, "/api/v1/companies", body, auth)
		require.Equal(t, http.StatusBadRequest, rec.Code, body)
		var p problem
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&p))
		assert.NotContains(t, p.Detail, "json:", "Go decoder errors must not leak: %s", body)
		assert.NotContains(t, p.Detail, "bad body", body)
		if want != "" {
			require.Len(t, p.Errors, 1, body)
			assert.Equal(t, want, p.Errors[0].Field+" "+p.Errors[0].Message, body)
		}
	}

	rec = do(h, http.MethodPost, "/api/v1/companies", `{"name":"`+strings.Repeat("a", maxBodyBytes)+`"}`, auth)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	headers := map[string]string{"Authorization": auth["Authorization"], "Content-Type": "text/plain"}
	rec = do(h, http.MethodPost, "/api/v1/companies", valid, headers)
	assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
}

func TestErrorMapping(t *testing.T) {
	auth := "Bearer " + token(t, testSecret, time.Now().Add(time.Hour))
	path := "/api/v1/companies/" + uuid.NewString()

	tests := []struct {
		err  error
		want int
	}{
		{err: domain.ErrNotFound, want: http.StatusNotFound},
		{err: domain.ErrNameTaken, want: http.StatusConflict},
		{err: domain.ErrVersionMismatch, want: http.StatusPreconditionFailed},
		{err: context.DeadlineExceeded, want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		h := newTestRouter(t, &fakeService{err: tt.err})
		rec := do(h, http.MethodPatch, path, `{"name":"X"}`, map[string]string{"Authorization": auth, "If-Match": `"1"`})
		assert.Equal(t, tt.want, rec.Code, tt.err.Error())
		assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	}
}

func TestOptimisticLockingHeaders(t *testing.T) {
	auth := "Bearer " + token(t, testSecret, time.Now().Add(time.Hour))
	path := "/api/v1/companies/" + uuid.NewString()

	svc := &fakeService{}
	h := newTestRouter(t, svc)

	rec := do(h, http.MethodDelete, path, "", map[string]string{"Authorization": auth})
	assert.Equal(t, http.StatusPreconditionRequired, rec.Code)

	rec = do(h, http.MethodDelete, path, "", map[string]string{"Authorization": auth, "If-Match": `W/"1"`})
	assert.Equal(t, http.StatusPreconditionFailed, rec.Code)

	rec = do(h, http.MethodDelete, path, "", map[string]string{"Authorization": auth, "If-Match": `"4"`})
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, int64(4), svc.gotVersion)
}

func TestPatchBody(t *testing.T) {
	auth := "Bearer " + token(t, testSecret, time.Now().Add(time.Hour))
	path := "/api/v1/companies/" + uuid.NewString()
	headers := map[string]string{"Authorization": auth, "If-Match": `"2"`, "Content-Type": "application/merge-patch+json"}

	svc := &fakeService{company: domain.Company{Version: 3}}
	h := newTestRouter(t, svc)

	rec := do(h, http.MethodPatch, path, `{"description":null,"registered":true}`, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, `"3"`, rec.Header().Get("ETag"))
	assert.True(t, svc.gotPatch.ClearDescription)
	assert.Equal(t, true, *svc.gotPatch.Registered)
	assert.Nil(t, svc.gotPatch.Name)

	for _, body := range []string{`{"name":null}`, `{"version":3}`, `{"foo":1}`, `{"amount_of_employees":"many"}`, `[1]`, `null`, `{} {}`} {
		svc.gotPatch = domain.PatchParams{}
		rec = do(h, http.MethodPatch, path, body, headers)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.True(t, svc.gotPatch.Empty(), "service must not be called: %s", body)
	}

	rec = do(h, http.MethodPatch, path, `{"id":"x"}`, headers)
	var p problem
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&p))
	require.Len(t, p.Errors, 1)
	assert.Equal(t, "is read-only", p.Errors[0].Message, "create and patch must agree")

	headers["Content-Type"] = "text/plain"
	rec = do(h, http.MethodPatch, path, `{}`, headers)
	assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
}

type panicService struct{ *fakeService }

func (panicService) Get(context.Context, uuid.UUID) (domain.Company, error) { panic("boom") }

func TestPanicIsRecoveredAndCounted(t *testing.T) {
	m := observability.NewMetrics()
	v, err := jwt.NewValidator(jwt.Config{Algorithm: "HS256", Secret: testSecret})
	require.NoError(t, err)
	h := NewRouter(Deps{Log: zap.NewNop(), Service: panicService{&fakeService{}}, Validator: v, Metrics: m})

	rec := do(h, http.MethodGet, "/api/v1/companies/"+uuid.NewString(), "", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, 1.0, testutil.ToFloat64(m.HTTPRequests.WithLabelValues(http.MethodGet, "/api/v1/companies/{id}", "500")))
}

func TestMetricsOnlyOnAdminRouter(t *testing.T) {
	m := observability.NewMetrics()
	rec := do(newTestRouter(t, &fakeService{}), http.MethodGet, "/metrics", "", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = do(NewAdminRouter(m), http.MethodGet, "/metrics", "", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "companies_outbox_published_total")
}

func TestAuthSchemeIsCaseInsensitive(t *testing.T) {
	h := newTestRouter(t, &fakeService{})
	tok := token(t, testSecret, time.Now().Add(time.Hour))
	path := "/api/v1/companies/" + uuid.NewString()
	for _, header := range []string{"bearer " + tok, "BEARER " + tok} {
		rec := do(h, http.MethodDelete, path, "", map[string]string{"Authorization": header, "If-Match": `"1"`})
		assert.Equal(t, http.StatusNoContent, rec.Code, header)
	}
	rec := do(h, http.MethodDelete, path, "", map[string]string{"Authorization": "Basic " + tok, "If-Match": `"1"`})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestReadinessHidesErrors(t *testing.T) {
	h := NewRouter(Deps{
		Log: zap.NewNop(), Service: &fakeService{}, Metrics: observability.NewMetrics(),
		Ready: map[string]ReadinessCheck{
			"postgres": func(context.Context) error { return errors.New("dial tcp 10.0.0.5:5432: connection refused") },
		},
	})
	rec := do(h, http.MethodGet, "/ready", "", nil)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.JSONEq(t, `{"postgres":"fail"}`, rec.Body.String())
}
