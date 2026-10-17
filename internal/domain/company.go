package domain

import (
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	NameMaxLen        = 15
	DescriptionMaxLen = 3000
	// EmployeesMax matches the integer column in Postgres.
	EmployeesMax = math.MaxInt32
)

type CompanyType string

const (
	CompanyTypeCorporations       CompanyType = "Corporations"
	CompanyTypeNonProfit          CompanyType = "NonProfit"
	CompanyTypeCooperative        CompanyType = "Cooperative"
	CompanyTypeSoleProprietorship CompanyType = "Sole Proprietorship"
)

func (t CompanyType) Valid() bool {
	switch t {
	case CompanyTypeCorporations, CompanyTypeNonProfit, CompanyTypeCooperative, CompanyTypeSoleProprietorship:
		return true
	}
	return false
}

type Company struct {
	ID                uuid.UUID
	Name              string
	Description       *string
	AmountOfEmployees int
	Registered        bool
	Type              CompanyType
	// Version is incremented on every update and backs optimistic locking.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateParams holds user-supplied fields of a new company.
type CreateParams struct {
	Name              string
	Description       *string
	AmountOfEmployees int
	Registered        bool
	Type              CompanyType
}

// PatchParams holds a partial update. Nil fields stay unchanged.
// ClearDescription removes the description, since nil already means "unchanged".
type PatchParams struct {
	Name              *string
	Description       *string
	ClearDescription  bool
	AmountOfEmployees *int
	Registered        *bool
	Type              *CompanyType
}

func (p PatchParams) Empty() bool {
	return p.Name == nil && p.Description == nil && !p.ClearDescription &&
		p.AmountOfEmployees == nil && p.Registered == nil && p.Type == nil
}

func NewCompany(p CreateParams, now time.Time) (Company, error) {
	c := Company{
		ID:                uuid.Must(uuid.NewV7()),
		Name:              strings.TrimSpace(p.Name),
		Description:       p.Description,
		AmountOfEmployees: p.AmountOfEmployees,
		Registered:        p.Registered,
		Type:              p.Type,
		Version:           1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := c.Validate(); err != nil {
		return Company{}, err
	}
	return c, nil
}

// Apply returns a copy of the company with the patch applied and validated.
func (c Company) Apply(p PatchParams, now time.Time) (Company, error) {
	if p.Name != nil {
		c.Name = strings.TrimSpace(*p.Name)
	}
	if p.ClearDescription {
		c.Description = nil
	} else if p.Description != nil {
		c.Description = p.Description
	}
	if p.AmountOfEmployees != nil {
		c.AmountOfEmployees = *p.AmountOfEmployees
	}
	if p.Registered != nil {
		c.Registered = *p.Registered
	}
	if p.Type != nil {
		c.Type = *p.Type
	}
	c.UpdatedAt = now
	if err := c.Validate(); err != nil {
		return Company{}, err
	}
	return c, nil
}

func (c Company) Validate() error {
	var errs ValidationError
	switch n := utf8.RuneCountInString(c.Name); {
	case n == 0:
		errs.Add("name", "is required")
	case n > NameMaxLen:
		errs.Add("name", "must be at most 15 characters")
	case strings.ContainsFunc(c.Name, unicode.IsControl):
		errs.Add("name", "must not contain control characters")
	}
	if c.Description != nil {
		switch {
		case utf8.RuneCountInString(*c.Description) > DescriptionMaxLen:
			errs.Add("description", "must be at most 3000 characters")
		case strings.ContainsFunc(*c.Description, isForbiddenInText):
			errs.Add("description", "must not contain control characters except tab and line breaks")
		}
	}
	switch {
	case c.AmountOfEmployees < 0:
		errs.Add("amount_of_employees", "must not be negative")
	case c.AmountOfEmployees > EmployeesMax:
		errs.Add("amount_of_employees", "must be at most 2147483647")
	}
	if !c.Type.Valid() {
		errs.Add("type", "must be one of: Corporations, NonProfit, Cooperative, Sole Proprietorship")
	}
	if len(errs.Fields) > 0 {
		return &errs
	}
	return nil
}

// isForbiddenInText rejects control characters, including NUL which Postgres cannot store, but keeps formatting ones.
func isForbiddenInText(r rune) bool {
	return unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r'
}
