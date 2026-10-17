package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bashkirian/companies-service/internal/domain"
	"github.com/bashkirian/companies-service/internal/usecase/outbox"
)

// EventPayload is the published event contract.
type EventPayload struct {
	EventID    uuid.UUID      `json:"event_id"`
	EventType  string         `json:"event_type"`
	OccurredAt time.Time      `json:"occurred_at"`
	Company    CompanyPayload `json:"company"`
}

type CompanyPayload struct {
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

type OutboxRepo struct {
	pool *pgxpool.Pool
}

func NewOutboxRepo(pool *pgxpool.Pool) *OutboxRepo {
	return &OutboxRepo{pool: pool}
}

func (r *OutboxRepo) Add(ctx context.Context, e domain.Event) error {
	c := e.Company
	payload, err := json.Marshal(EventPayload{
		EventID:    e.ID,
		EventType:  string(e.Type),
		OccurredAt: e.OccurredAt,
		Company: CompanyPayload{
			ID:                c.ID,
			Name:              c.Name,
			Description:       c.Description,
			AmountOfEmployees: c.AmountOfEmployees,
			Registered:        c.Registered,
			Type:              string(c.Type),
			Version:           c.Version,
			CreatedAt:         c.CreatedAt,
			UpdatedAt:         c.UpdatedAt,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	_, err = conn(ctx, r.pool).Exec(ctx, `
		INSERT INTO outbox (id, aggregate_id, event_type, payload, created_at) VALUES ($1, $2, $3, $4, $5)`,
		e.ID, c.ID, string(e.Type), payload, e.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert outbox: %w", err)
	}
	return nil
}

func (r *OutboxRepo) LockPending(ctx context.Context, limit int) ([]outbox.Message, error) {
	rows, err := conn(ctx, r.pool).Query(ctx, `
		SELECT id, aggregate_id, event_type, payload
		FROM outbox
		ORDER BY created_at, id
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("select outbox: %w", err)
	}
	defer rows.Close()

	var msgs []outbox.Message
	for rows.Next() {
		var (
			m           outbox.Message
			aggregateID uuid.UUID
		)
		if err := rows.Scan(&m.ID, &aggregateID, &m.Type, &m.Payload); err != nil {
			return nil, fmt.Errorf("scan outbox: %w", err)
		}
		m.Key = aggregateID.String()
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox: %w", err)
	}
	return msgs, nil
}

func (r *OutboxRepo) Delete(ctx context.Context, ids []uuid.UUID) error {
	if _, err := conn(ctx, r.pool).Exec(ctx, `DELETE FROM outbox WHERE id = ANY($1)`, ids); err != nil {
		return fmt.Errorf("delete outbox: %w", err)
	}
	return nil
}

func (r *OutboxRepo) Backlog(ctx context.Context) (int, time.Time, error) {
	var (
		pending int
		oldest  *time.Time
	)
	if err := conn(ctx, r.pool).QueryRow(ctx, `SELECT count(*), min(created_at) FROM outbox`).Scan(&pending, &oldest); err != nil {
		return 0, time.Time{}, fmt.Errorf("outbox backlog: %w", err)
	}
	if oldest == nil {
		return 0, time.Time{}, nil
	}
	return pending, *oldest, nil
}
