package company

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bashkirian/companies-service/internal/domain"
)

type fakeRepo struct {
	items map[uuid.UUID]domain.Company
}

func (r *fakeRepo) Create(_ context.Context, c domain.Company) error {
	r.items[c.ID] = c
	return nil
}

func (r *fakeRepo) Get(_ context.Context, id uuid.UUID) (domain.Company, error) {
	c, ok := r.items[id]
	if !ok {
		return domain.Company{}, domain.ErrNotFound
	}
	return c, nil
}

func (r *fakeRepo) Update(_ context.Context, c domain.Company, expected int64) error {
	if r.items[c.ID].Version != expected {
		return domain.ErrVersionMismatch
	}
	r.items[c.ID] = c
	return nil
}

func (r *fakeRepo) Delete(_ context.Context, id uuid.UUID, expected int64) error {
	if r.items[id].Version != expected {
		return domain.ErrVersionMismatch
	}
	delete(r.items, id)
	return nil
}

type fakeOutbox struct {
	events []domain.Event
	err    error
}

func (o *fakeOutbox) Add(_ context.Context, e domain.Event) error {
	if o.err != nil {
		return o.err
	}
	o.events = append(o.events, e)
	return nil
}

type fakeTx struct{}

func (fakeTx) Do(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

func newTestService() (*Service, *fakeRepo, *fakeOutbox) {
	repo := &fakeRepo{items: map[uuid.UUID]domain.Company{}}
	ob := &fakeOutbox{}
	return NewService(repo, ob, fakeTx{}), repo, ob
}

func create(t *testing.T, s *Service) domain.Company {
	t.Helper()
	c, err := s.Create(context.Background(), domain.CreateParams{
		Name: "Acme", AmountOfEmployees: 3, Type: domain.CompanyTypeNonProfit,
	})
	require.NoError(t, err)
	return c
}

func TestServiceCreateEmitsEvent(t *testing.T) {
	s, repo, ob := newTestService()
	c := create(t, s)

	assert.Contains(t, repo.items, c.ID)
	require.Len(t, ob.events, 1)
	assert.Equal(t, domain.EventCompanyCreated, ob.events[0].Type)
	assert.Equal(t, c, ob.events[0].Company)
}

func TestServiceCreateFailsWhenOutboxFails(t *testing.T) {
	s, _, ob := newTestService()
	ob.err = errors.New("boom")

	_, err := s.Create(context.Background(), domain.CreateParams{Name: "Acme", Type: domain.CompanyTypeNonProfit})
	assert.ErrorIs(t, err, ob.err)
}

func TestServicePatch(t *testing.T) {
	s, _, ob := newTestService()
	c := create(t, s)
	name := "Beta"

	updated, err := s.Patch(context.Background(), c.ID, 1, domain.PatchParams{Name: &name})
	require.NoError(t, err)
	assert.Equal(t, "Beta", updated.Name)
	assert.Equal(t, int64(2), updated.Version)
	require.Len(t, ob.events, 2)
	assert.Equal(t, domain.EventCompanyUpdated, ob.events[1].Type)

	_, err = s.Patch(context.Background(), c.ID, 1, domain.PatchParams{Name: &name})
	assert.ErrorIs(t, err, domain.ErrVersionMismatch)
}

func TestServicePatchEmptyIsNoop(t *testing.T) {
	s, _, ob := newTestService()
	c := create(t, s)

	got, err := s.Patch(context.Background(), c.ID, 1, domain.PatchParams{})
	require.NoError(t, err)
	assert.Equal(t, c, got)
	assert.Len(t, ob.events, 1, "no update event for an empty patch")
}

func TestServiceDelete(t *testing.T) {
	s, repo, ob := newTestService()
	c := create(t, s)

	assert.ErrorIs(t, s.Delete(context.Background(), c.ID, 7), domain.ErrVersionMismatch)
	require.NoError(t, s.Delete(context.Background(), c.ID, 1))
	assert.NotContains(t, repo.items, c.ID)
	require.Len(t, ob.events, 2)
	assert.Equal(t, domain.EventCompanyDeleted, ob.events[1].Type)
	deleted := ob.events[1].Company
	assert.Equal(t, c.ID, deleted.ID)
	assert.Equal(t, c.Name, deleted.Name)
	assert.Equal(t, int64(2), deleted.Version, "delete event must have a newer version than the last update")

	assert.ErrorIs(t, s.Delete(context.Background(), c.ID, 1), domain.ErrNotFound)
}
