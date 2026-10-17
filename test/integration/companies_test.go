//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

type company struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Description       *string `json:"description"`
	AmountOfEmployees int     `json:"amount_of_employees"`
	Registered        bool    `json:"registered"`
	Type              string  `json:"type"`
	Version           int64   `json:"version"`
}

type event struct {
	EventID   string  `json:"event_id"`
	EventType string  `json:"event_type"`
	Company   company `json:"company"`
}

func bearer(t *testing.T) string {
	t.Helper()
	s, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, gojwt.RegisteredClaims{
		Subject:   "integration-user",
		Issuer:    "integration",
		Audience:  gojwt.ClaimStrings{"companies"},
		ExpiresAt: gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString([]byte(jwtSecret))
	require.NoError(t, err)
	return "Bearer " + s
}

// reply keeps only what tests inspect; the body is already read and closed.
type reply struct {
	StatusCode int
	Header     http.Header
}

func call(t *testing.T, method, path, body string, headers map[string]string) (reply, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, baseURL+path, strings.NewReader(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return reply{StatusCode: resp.StatusCode, Header: resp.Header}, data
}

// uniqueName fits the 15 character limit and avoids clashes between tests.
func uniqueName(prefix string) string {
	return prefix + strings.ReplaceAll(uuid.NewString(), "-", "")[:15-len(prefix)]
}

func createCompany(t *testing.T, name string) (company, string) {
	t.Helper()
	resp, body := call(t, http.MethodPost, "/api/v1/companies",
		fmt.Sprintf(`{"name":%q,"description":"desc","amount_of_employees":10,"registered":false,"type":"Corporations"}`, name),
		map[string]string{"Authorization": bearer(t)})
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(body))
	var c company
	require.NoError(t, json.Unmarshal(body, &c))
	return c, resp.Header.Get("ETag")
}

func TestCompanyLifecycleProducesEvents(t *testing.T) {
	auth := bearer(t)
	c, etag := createCompany(t, uniqueName("life"))
	assert.Equal(t, `"1"`, etag)

	resp, body := call(t, http.MethodGet, "/api/v1/companies/"+c.ID, "", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got company
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, c, got)

	resp, body = call(t, http.MethodPatch, "/api/v1/companies/"+c.ID,
		`{"description":null,"amount_of_employees":42,"type":"NonProfit"}`,
		map[string]string{"Authorization": auth, "If-Match": etag, "Content-Type": "application/merge-patch+json"})
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Nil(t, got.Description)
	assert.Equal(t, 42, got.AmountOfEmployees)
	assert.Equal(t, "NonProfit", got.Type)
	assert.Equal(t, int64(2), got.Version)
	assert.Equal(t, `"2"`, resp.Header.Get("ETag"))

	resp, _ = call(t, http.MethodDelete, "/api/v1/companies/"+c.ID, "", map[string]string{"Authorization": auth, "If-Match": etag})
	assert.Equal(t, http.StatusPreconditionFailed, resp.StatusCode, "stale ETag must be rejected")

	resp, _ = call(t, http.MethodDelete, "/api/v1/companies/"+c.ID, "", map[string]string{"Authorization": auth, "If-Match": `"2"`})
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	resp, _ = call(t, http.MethodGet, "/api/v1/companies/"+c.ID, "", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	events := consumeEvents(t, c.ID, 3)
	require.Len(t, events, 3)
	assert.Equal(t, []string{"company.created", "company.updated", "company.deleted"},
		[]string{events[0].EventType, events[1].EventType, events[2].EventType})
	assert.Equal(t, []int64{1, 2, 3},
		[]int64{events[0].Company.Version, events[1].Company.Version, events[2].Company.Version})
	assert.Equal(t, 42, events[2].Company.AmountOfEmployees, "delete event carries the last state")
}

func TestNameIsUniqueCaseInsensitive(t *testing.T) {
	name := uniqueName("uniq")
	createCompany(t, name)

	resp, body := call(t, http.MethodPost, "/api/v1/companies",
		fmt.Sprintf(`{"name":%q,"amount_of_employees":1,"registered":true,"type":"Cooperative"}`, strings.ToUpper(name)),
		map[string]string{"Authorization": bearer(t)})
	assert.Equal(t, http.StatusConflict, resp.StatusCode, string(body))

	other, etag := createCompany(t, uniqueName("uniq"))
	resp, _ = call(t, http.MethodPatch, "/api/v1/companies/"+other.ID, fmt.Sprintf(`{"name":%q}`, name),
		map[string]string{"Authorization": bearer(t), "If-Match": etag})
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestConcurrentPatchesWithSameVersion(t *testing.T) {
	c, etag := createCompany(t, uniqueName("race"))
	auth := bearer(t)

	const workers = 5
	codes := make([]int, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			resp, _ := call(t, http.MethodPatch, "/api/v1/companies/"+c.ID,
				fmt.Sprintf(`{"amount_of_employees":%d}`, i+100),
				map[string]string{"Authorization": auth, "If-Match": etag})
			codes[i] = resp.StatusCode
		})
	}
	wg.Wait()

	var ok, conflict int
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusPreconditionFailed:
			conflict++
		}
	}
	assert.Equal(t, 1, ok, "exactly one writer wins: %v", codes)
	assert.Equal(t, workers-1, conflict, "%v", codes)
}

func TestValidationAndAuth(t *testing.T) {
	resp, _ := call(t, http.MethodPost, "/api/v1/companies", `{"name":"x","amount_of_employees":1,"registered":true,"type":"Corporations"}`, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp, body := call(t, http.MethodPost, "/api/v1/companies",
		`{"name":"this name is too long","amount_of_employees":-1,"registered":true,"type":"LLC"}`,
		map[string]string{"Authorization": bearer(t)})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))
	for _, field := range []string{"name", "amount_of_employees", "type"} {
		assert.True(t, bytes.Contains(body, []byte(`"field":"`+field+`"`)), field)
	}

	resp, _ = call(t, http.MethodGet, "/api/v1/companies/not-a-uuid", "", nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// Values the database cannot store must be rejected as 400, not surface as 500.
func TestValuesPostgresCannotStore(t *testing.T) {
	auth := map[string]string{"Authorization": bearer(t)}
	bodies := map[string]string{
		"employees over int32": fmt.Sprintf(`{"name":%q,"amount_of_employees":3000000000,"registered":true,"type":"Corporations"}`, uniqueName("big")),
		"NUL in name":          `{"name":"Ac\u0000me","amount_of_employees":1,"registered":true,"type":"Corporations"}`,
		"NUL in description":   fmt.Sprintf(`{"name":%q,"description":"a\u0000b","amount_of_employees":1,"registered":true,"type":"Corporations"}`, uniqueName("nul")),
	}
	for name, body := range bodies {
		resp, data := call(t, http.MethodPost, "/api/v1/companies", body, auth)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "%s: %s", name, data)
	}

	c, etag := createCompany(t, uniqueName("big"))
	resp, data := call(t, http.MethodPatch, "/api/v1/companies/"+c.ID, `{"amount_of_employees":3000000000}`,
		map[string]string{"Authorization": bearer(t), "If-Match": etag})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, string(data))
}

func TestMetricsOnAdminPort(t *testing.T) {
	resp, _ := call(t, http.MethodGet, "/metrics", "", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "metrics must not be on the public port")

	req, err := http.NewRequest(http.MethodGet, adminURL+"/metrics", nil)
	require.NoError(t, err)
	r, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = r.Body.Close() }()
	body, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, r.StatusCode)
	assert.Contains(t, string(body), "companies_outbox_pending")
	assert.Contains(t, string(body), "companies_outbox_oldest_age_seconds")
}

// consumeEvents reads the topic from the start and returns the events of one company in order.
func consumeEvents(t *testing.T, companyID string, want int) []event {
	t.Helper()
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	require.NoError(t, err)
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var events []event
	for len(events) < want {
		fetches := cl.PollFetches(ctx)
		if ctx.Err() != nil {
			break
		}
		require.NoError(t, fetches.Err())
		fetches.EachRecord(func(r *kgo.Record) {
			if string(r.Key) != companyID {
				return
			}
			assert.True(t, strings.HasPrefix(string(r.Value), `{"event_id":"`),
				"payload must reach Kafka exactly as serialized: %s", r.Value)
			var e event
			require.NoError(t, json.Unmarshal(r.Value, &e))
			events = append(events, e)
		})
	}
	return events
}
