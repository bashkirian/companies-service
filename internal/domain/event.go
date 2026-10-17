package domain

import (
	"time"

	"github.com/google/uuid"
)

type EventType string

const (
	EventCompanyCreated EventType = "company.created"
	EventCompanyUpdated EventType = "company.updated"
	EventCompanyDeleted EventType = "company.deleted"
)

// Event describes a company mutation. For deletions Company is the last state with the version bumped once more.
type Event struct {
	ID         uuid.UUID
	Type       EventType
	OccurredAt time.Time
	Company    Company
}

func NewEvent(t EventType, c Company, now time.Time) Event {
	return Event{ID: uuid.Must(uuid.NewV7()), Type: t, OccurredAt: now, Company: c}
}
