package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeStore struct {
	pending []Message
	deleted []uuid.UUID
}

func (s *fakeStore) LockPending(_ context.Context, limit int) ([]Message, error) {
	return s.pending[:min(limit, len(s.pending))], nil
}

func (s *fakeStore) Delete(_ context.Context, ids []uuid.UUID) error {
	s.deleted = append(s.deleted, ids...)
	return nil
}

func (s *fakeStore) Backlog(context.Context) (int, time.Time, error) {
	if len(s.pending) == 0 {
		return 0, time.Time{}, nil
	}
	return len(s.pending), time.Now().Add(-time.Minute), nil
}

type fakePublisher struct {
	got []Message
	err error
}

func (p *fakePublisher) Publish(_ context.Context, msgs []Message) error {
	if p.err != nil {
		return p.err
	}
	p.got = append(p.got, msgs...)
	return nil
}

type fakeTx struct{}

func (fakeTx) Do(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type recMetrics struct {
	pending chan int
	age     time.Duration
}

func (*recMetrics) Published(int) {}
func (*recMetrics) Failed()       {}
func (m *recMetrics) Backlog(pending int, age time.Duration) {
	m.age = age
	m.pending <- pending
}

func newRelayWithMetrics(store Store, pub Publisher, m Metrics) *Relay {
	return NewRelay(store, pub, fakeTx{}, m, Config{
		PollInterval: time.Second, BatchSize: 2, PublishTimeout: time.Second, BacklogInterval: time.Hour,
	}, zap.NewNop())
}

func newRelay(store Store, pub Publisher) *Relay {
	return newRelayWithMetrics(store, pub, &recMetrics{pending: make(chan int, 1)})
}

func TestFlushPublishesAndDeletes(t *testing.T) {
	msgs := []Message{{ID: uuid.New()}, {ID: uuid.New()}, {ID: uuid.New()}}
	store := &fakeStore{pending: msgs}
	pub := &fakePublisher{}

	n, err := newRelay(store, pub).Flush(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, msgs[:2], pub.got)
	assert.Equal(t, []uuid.UUID{msgs[0].ID, msgs[1].ID}, store.deleted)
}

func TestFlushKeepsMessagesWhenPublishFails(t *testing.T) {
	store := &fakeStore{pending: []Message{{ID: uuid.New()}}}
	pub := &fakePublisher{err: errors.New("broker down")}

	_, err := newRelay(store, pub).Flush(context.Background())
	require.ErrorIs(t, err, pub.err)
	assert.Empty(t, store.deleted)
}

func TestReportBacklog(t *testing.T) {
	m := &recMetrics{pending: make(chan int)}
	store := &fakeStore{pending: []Message{{ID: uuid.New()}, {ID: uuid.New()}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- newRelayWithMetrics(store, &fakePublisher{}, m).ReportBacklog(ctx) }()

	assert.Equal(t, 2, <-m.pending)
	assert.GreaterOrEqual(t, m.age, time.Minute)
	cancel()
	require.NoError(t, <-done)
}
