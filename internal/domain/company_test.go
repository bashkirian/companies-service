package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bashkirian/companies-service/internal/domain"
)

func ptr[T any](v T) *T { return &v }

func validParams() domain.CreateParams {
	return domain.CreateParams{
		Name:              "Acme",
		AmountOfEmployees: 10,
		Registered:        true,
		Type:              domain.CompanyTypeCorporations,
	}
}

func TestNewCompany(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		name       string
		mutate     func(p *domain.CreateParams)
		wantFields []string
	}{
		{name: "valid", mutate: func(*domain.CreateParams) {}},
		{name: "name trimmed to fit", mutate: func(p *domain.CreateParams) { p.Name = "  " + strings.Repeat("a", 15) + "  " }},
		{name: "15 multibyte runes", mutate: func(p *domain.CreateParams) { p.Name = strings.Repeat("ж", 15) }},
		{name: "empty name", mutate: func(p *domain.CreateParams) { p.Name = "   " }, wantFields: []string{"name"}},
		{name: "long name", mutate: func(p *domain.CreateParams) { p.Name = strings.Repeat("a", 16) }, wantFields: []string{"name"}},
		{name: "long description", mutate: func(p *domain.CreateParams) { p.Description = ptr(strings.Repeat("d", 3001)) }, wantFields: []string{"description"}},
		{name: "negative employees", mutate: func(p *domain.CreateParams) { p.AmountOfEmployees = -1 }, wantFields: []string{"amount_of_employees"}},
		{name: "employees over int32", mutate: func(p *domain.CreateParams) { p.AmountOfEmployees = 3_000_000_000 }, wantFields: []string{"amount_of_employees"}},
		{name: "employees at int32 max", mutate: func(p *domain.CreateParams) { p.AmountOfEmployees = 2147483647 }},
		{name: "NUL in name", mutate: func(p *domain.CreateParams) { p.Name = "Ac\x00me" }, wantFields: []string{"name"}},
		{name: "newline in name", mutate: func(p *domain.CreateParams) { p.Name = "Ac\nme" }, wantFields: []string{"name"}},
		{name: "NUL in description", mutate: func(p *domain.CreateParams) { p.Description = ptr("a\x00b") }, wantFields: []string{"description"}},
		{name: "line breaks in description", mutate: func(p *domain.CreateParams) { p.Description = ptr("line1\r\nline2\tend") }},
		{name: "unknown type", mutate: func(p *domain.CreateParams) { p.Type = "LLC" }, wantFields: []string{"type"}},
		{
			name: "several errors",
			mutate: func(p *domain.CreateParams) {
				p.Name = ""
				p.Type = ""
			},
			wantFields: []string{"name", "type"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validParams()
			tt.mutate(&p)

			c, err := domain.NewCompany(p, now)
			if len(tt.wantFields) == 0 {
				require.NoError(t, err)
				assert.NotZero(t, c.ID)
				assert.Equal(t, int64(1), c.Version)
				assert.Equal(t, now, c.CreatedAt)
				return
			}
			var verr *domain.ValidationError
			require.ErrorAs(t, err, &verr)
			got := make([]string, 0, len(verr.Fields))
			for _, f := range verr.Fields {
				got = append(got, f.Field)
			}
			assert.Equal(t, tt.wantFields, got)
		})
	}
}

func TestCompanyApply(t *testing.T) {
	now := time.Now().UTC()
	base, err := domain.NewCompany(domain.CreateParams{
		Name: "Acme", Description: ptr("old"), AmountOfEmployees: 1, Type: domain.CompanyTypeCooperative,
	}, now)
	require.NoError(t, err)

	later := now.Add(time.Minute)
	updated, err := base.Apply(domain.PatchParams{
		Name:              ptr(" Beta "),
		AmountOfEmployees: ptr(5),
		Registered:        ptr(true),
		ClearDescription:  true,
	}, later)
	require.NoError(t, err)

	assert.Equal(t, "Beta", updated.Name)
	assert.Nil(t, updated.Description)
	assert.Equal(t, 5, updated.AmountOfEmployees)
	assert.True(t, updated.Registered)
	assert.Equal(t, domain.CompanyTypeCooperative, updated.Type)
	assert.Equal(t, later, updated.UpdatedAt)
	assert.Equal(t, "Acme", base.Name, "original must stay untouched")

	_, err = base.Apply(domain.PatchParams{Type: ptr(domain.CompanyType("bad"))}, later)
	var verr *domain.ValidationError
	assert.ErrorAs(t, err, &verr)
}
