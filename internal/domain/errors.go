package domain

import (
	"errors"
	"strings"
)

var (
	ErrNotFound        = errors.New("company not found")
	ErrNameTaken       = errors.New("company name already taken")
	ErrVersionMismatch = errors.New("company version mismatch")
)

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Add(field, msg string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Message: msg})
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Field+" "+f.Message)
	}
	return "validation failed: " + strings.Join(parts, "; ")
}
