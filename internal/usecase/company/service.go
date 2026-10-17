package company

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/bashkirian/companies-service/internal/domain"
)

type Repository interface {
	Create(ctx context.Context, c domain.Company) error
	Get(ctx context.Context, id uuid.UUID) (domain.Company, error)
	// Update stores c if the stored version equals expectedVersion.
	Update(ctx context.Context, c domain.Company, expectedVersion int64) error
	// Delete removes the company if the stored version equals expectedVersion.
	Delete(ctx context.Context, id uuid.UUID, expectedVersion int64) error
}

type EventOutbox interface {
	Add(ctx context.Context, e domain.Event) error
}

// TxManager runs fn in one DB transaction carried through ctx.
type TxManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type Service struct {
	repo   Repository
	outbox EventOutbox
	tx     TxManager
	now    func() time.Time
}

func NewService(repo Repository, outbox EventOutbox, tx TxManager) *Service {
	return &Service{
		repo:   repo,
		outbox: outbox,
		tx:     tx,
		now:    func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
	}
}

func (s *Service) Create(ctx context.Context, p domain.CreateParams) (domain.Company, error) {
	c, err := domain.NewCompany(p, s.now())
	if err != nil {
		return domain.Company{}, err
	}
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		if err := s.repo.Create(ctx, c); err != nil {
			return err
		}
		return s.outbox.Add(ctx, domain.NewEvent(domain.EventCompanyCreated, c, c.CreatedAt))
	})
	if err != nil {
		return domain.Company{}, fmt.Errorf("create company: %w", err)
	}
	return c, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (domain.Company, error) {
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.Company{}, fmt.Errorf("get company: %w", err)
	}
	return c, nil
}

func (s *Service) Patch(ctx context.Context, id uuid.UUID, expectedVersion int64, p domain.PatchParams) (domain.Company, error) {
	var updated domain.Company
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		current, err := s.repo.Get(ctx, id)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return domain.ErrVersionMismatch
		}
		if p.Empty() {
			updated = current
			return nil
		}
		updated, err = current.Apply(p, s.now())
		if err != nil {
			return err
		}
		updated.Version = current.Version + 1
		if err := s.repo.Update(ctx, updated, expectedVersion); err != nil {
			return err
		}
		return s.outbox.Add(ctx, domain.NewEvent(domain.EventCompanyUpdated, updated, updated.UpdatedAt))
	})
	if err != nil {
		return domain.Company{}, fmt.Errorf("patch company: %w", err)
	}
	return updated, nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID, expectedVersion int64) error {
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		current, err := s.repo.Get(ctx, id)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return domain.ErrVersionMismatch
		}
		if err := s.repo.Delete(ctx, id, expectedVersion); err != nil {
			return err
		}
		// Deletion is a state change too: bump the version so consumers ordering by it keep the event.
		tombstone := current
		tombstone.Version++
		tombstone.UpdatedAt = s.now()
		return s.outbox.Add(ctx, domain.NewEvent(domain.EventCompanyDeleted, tombstone, tombstone.UpdatedAt))
	})
	if err != nil {
		return fmt.Errorf("delete company: %w", err)
	}
	return nil
}
