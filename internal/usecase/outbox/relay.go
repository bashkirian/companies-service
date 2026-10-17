package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Message struct {
	ID      uuid.UUID
	Key     string
	Type    string
	Payload []byte
}

type Store interface {
	// LockPending returns up to limit oldest unpublished messages locked for the current transaction.
	LockPending(ctx context.Context, limit int) ([]Message, error)
	Delete(ctx context.Context, ids []uuid.UUID) error
	// Backlog returns the number of unpublished messages and the creation time of the oldest one.
	Backlog(ctx context.Context) (pending int, oldest time.Time, err error)
}

type Publisher interface {
	Publish(ctx context.Context, msgs []Message) error
}

type TxManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type Metrics interface {
	Published(n int)
	Failed()
	Backlog(pending int, oldestAge time.Duration)
}

type Config struct {
	PollInterval   time.Duration
	BatchSize      int
	PublishTimeout time.Duration
	// BacklogInterval is how often backlog metrics are refreshed.
	BacklogInterval time.Duration
}

// Relay moves messages from the outbox table to the broker with at-least-once delivery.
// Several replicas may run concurrently: rows are locked with SKIP LOCKED.
type Relay struct {
	store     Store
	publisher Publisher
	tx        TxManager
	metrics   Metrics
	cfg       Config
	log       *zap.Logger
}

func NewRelay(store Store, publisher Publisher, tx TxManager, metrics Metrics, cfg Config, log *zap.Logger) *Relay {
	return &Relay{store: store, publisher: publisher, tx: tx, metrics: metrics, cfg: cfg, log: log}
}

// Run polls until ctx is canceled. A full batch triggers the next poll immediately.
func (r *Relay) Run(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}

		n, err := r.Flush(ctx)
		next := r.cfg.PollInterval
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil
			}
			r.metrics.Failed()
			r.log.Error("outbox flush failed", zap.Error(err))
		case n == r.cfg.BatchSize:
			next = 0
		}
		timer.Reset(next)
	}
}

// Flush publishes one batch and returns how many messages were sent.
func (r *Relay) Flush(ctx context.Context) (int, error) {
	var sent int
	err := r.tx.Do(ctx, func(ctx context.Context) error {
		msgs, err := r.store.LockPending(ctx, r.cfg.BatchSize)
		if err != nil {
			return fmt.Errorf("lock pending: %w", err)
		}
		if len(msgs) == 0 {
			return nil
		}

		pubCtx, cancel := context.WithTimeout(ctx, r.cfg.PublishTimeout)
		defer cancel()
		if err := r.publisher.Publish(pubCtx, msgs); err != nil {
			return fmt.Errorf("publish: %w", err)
		}

		ids := make([]uuid.UUID, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID
		}
		if err := r.store.Delete(ctx, ids); err != nil {
			return fmt.Errorf("delete published: %w", err)
		}
		sent = len(msgs)
		return nil
	})
	if err != nil {
		return 0, err
	}
	if sent > 0 {
		r.metrics.Published(sent)
	}
	return sent, nil
}

// ReportBacklog refreshes backlog metrics until ctx is canceled, so a stuck broker is visible before the table grows large.
func (r *Relay) ReportBacklog(ctx context.Context) error {
	ticker := time.NewTicker(r.cfg.BacklogInterval)
	defer ticker.Stop()
	for {
		pending, oldest, err := r.store.Backlog(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			r.log.Warn("outbox backlog query failed", zap.Error(err))
		case err == nil:
			var age time.Duration
			if pending > 0 {
				age = time.Since(oldest)
			}
			r.metrics.Backlog(pending, age)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
