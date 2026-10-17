package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/bashkirian/companies-service/internal/domain"
)

type companyResponse struct {
	ID                uuid.UUID `json:"id"`
	Name              string    `json:"name"`
	Description       *string   `json:"description"`
	AmountOfEmployees int       `json:"amount_of_employees"`
	Registered        bool      `json:"registered"`
	Type              string    `json:"type"`
	Version           int64     `json:"version"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func toResponse(c domain.Company) companyResponse {
	return companyResponse{
		ID:                c.ID,
		Name:              c.Name,
		Description:       c.Description,
		AmountOfEmployees: c.AmountOfEmployees,
		Registered:        c.Registered,
		Type:              string(c.Type),
		Version:           c.Version,
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
	}
}

// createRequest uses pointers to tell missing required fields from zero values.
type createRequest struct {
	Name              *string
	Description       *string
	AmountOfEmployees *int
	Registered        *bool
	Type              *string
}

func (r createRequest) toParams() (domain.CreateParams, error) {
	var errs domain.ValidationError
	if r.Name == nil {
		errs.Add("name", "is required")
	}
	if r.AmountOfEmployees == nil {
		errs.Add("amount_of_employees", "is required")
	}
	if r.Registered == nil {
		errs.Add("registered", "is required")
	}
	if r.Type == nil {
		errs.Add("type", "is required")
	}
	if len(errs.Fields) > 0 {
		return domain.CreateParams{}, &errs
	}
	return domain.CreateParams{
		Name:              *r.Name,
		Description:       r.Description,
		AmountOfEmployees: *r.AmountOfEmployees,
		Registered:        *r.Registered,
		Type:              domain.CompanyType(*r.Type),
	}, nil
}

var errBodyTooLarge = errors.New("body too large")

// badBodyError is a client mistake in the body syntax; its text is safe to return.
type badBodyError string

func (e badBodyError) Error() string { return string(e) }

var readOnlyFields = []string{"id", "version", "created_at", "updated_at"}

// decodeObject reads exactly one JSON object. Keys are matched exactly by the callers,
// unlike encoding/json struct decoding, which ignores case.
func decodeObject(body io.Reader) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(body)
	if err := dec.Decode(&raw); err != nil {
		return nil, syntaxErr(err, "body must be a JSON object")
	}
	if raw == nil { // literal null
		return nil, badBodyError("body must be a JSON object")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, syntaxErr(err, "body must contain a single JSON object")
	}
	return raw, nil
}

func syntaxErr(err error, msg string) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return errBodyTooLarge
	}
	return badBodyError(msg)
}

func decodeCreate(body io.Reader) (createRequest, error) {
	raw, err := decodeObject(body)
	if err != nil {
		return createRequest{}, err
	}

	var (
		r    createRequest
		errs domain.ValidationError
	)
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		val := raw[key]
		switch key {
		case "name":
			r.Name = decodeOptional[string](key, val, &errs)
		case "description":
			r.Description = decodeOptional[string](key, val, &errs)
		case "amount_of_employees":
			r.AmountOfEmployees = decodeOptional[int](key, val, &errs)
		case "registered":
			r.Registered = decodeOptional[bool](key, val, &errs)
		case "type":
			r.Type = decodeOptional[string](key, val, &errs)
		default:
			rejectField(key, &errs)
		}
	}
	if len(errs.Fields) > 0 {
		return createRequest{}, &errs
	}
	return r, nil
}

// decodePatch parses an RFC 7396 merge patch. Absent keys stay unchanged,
// null clears optional fields and is rejected for required ones.
func decodePatch(body io.Reader) (domain.PatchParams, error) {
	raw, err := decodeObject(body)
	if err != nil {
		return domain.PatchParams{}, err
	}

	var (
		p    domain.PatchParams
		errs domain.ValidationError
	)
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		val := raw[key]
		switch key {
		case "name":
			p.Name = decodeRequired[string](key, val, &errs)
		case "description":
			if isNull(val) {
				p.ClearDescription = true
				continue
			}
			p.Description = decodeRequired[string](key, val, &errs)
		case "amount_of_employees":
			p.AmountOfEmployees = decodeRequired[int](key, val, &errs)
		case "registered":
			p.Registered = decodeRequired[bool](key, val, &errs)
		case "type":
			if s := decodeRequired[string](key, val, &errs); s != nil {
				t := domain.CompanyType(*s)
				p.Type = &t
			}
		default:
			rejectField(key, &errs)
		}
	}
	if len(errs.Fields) > 0 {
		return domain.PatchParams{}, &errs
	}
	return p, nil
}

func rejectField(key string, errs *domain.ValidationError) {
	if slices.Contains(readOnlyFields, key) {
		errs.Add(key, "is read-only")
		return
	}
	errs.Add(key, "is unknown")
}

func isNull(val json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(val), []byte("null"))
}

// decodeOptional returns nil for null; the caller decides whether that is allowed.
func decodeOptional[T any](key string, val json.RawMessage, errs *domain.ValidationError) *T {
	if isNull(val) {
		return nil
	}
	var v T
	if err := json.Unmarshal(val, &v); err != nil {
		errs.Add(key, "has invalid type")
		return nil
	}
	return &v
}

func decodeRequired[T any](key string, val json.RawMessage, errs *domain.ValidationError) *T {
	if isNull(val) {
		errs.Add(key, "must not be null")
		return nil
	}
	return decodeOptional[T](key, val, errs)
}
