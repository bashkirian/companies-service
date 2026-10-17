package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bashkirian/companies-service/internal/domain"
)

const (
	pgUniqueViolation  = "23505"
	nameUniqConstraint = "companies_name_lower_key"
)

type CompanyRepo struct {
	pool *pgxpool.Pool
}

func NewCompanyRepo(pool *pgxpool.Pool) *CompanyRepo {
	return &CompanyRepo{pool: pool}
}

func (r *CompanyRepo) Create(ctx context.Context, c domain.Company) error {
	_, err := conn(ctx, r.pool).Exec(ctx, `
		INSERT INTO companies (id, name, description, amount_of_employees, registered, type, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		c.ID, c.Name, c.Description, c.AmountOfEmployees, c.Registered, string(c.Type), c.Version, c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return mapWriteErr(err)
	}
	return nil
}

func (r *CompanyRepo) Get(ctx context.Context, id uuid.UUID) (domain.Company, error) {
	var (
		c   domain.Company
		typ string
	)
	err := conn(ctx, r.pool).QueryRow(ctx, `
		SELECT id, name, description, amount_of_employees, registered, type, version, created_at, updated_at
		FROM companies WHERE id = $1`, id).
		Scan(&c.ID, &c.Name, &c.Description, &c.AmountOfEmployees, &c.Registered, &typ, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Company{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Company{}, fmt.Errorf("select company: %w", err)
	}
	c.Type = domain.CompanyType(typ)
	return c, nil
}

func (r *CompanyRepo) Update(ctx context.Context, c domain.Company, expectedVersion int64) error {
	tag, err := conn(ctx, r.pool).Exec(ctx, `
		UPDATE companies
		SET name = $3, description = $4, amount_of_employees = $5, registered = $6, type = $7, version = $8, updated_at = $9
		WHERE id = $1 AND version = $2`,
		c.ID, expectedVersion, c.Name, c.Description, c.AmountOfEmployees, c.Registered, string(c.Type), c.Version, c.UpdatedAt)
	if err != nil {
		return mapWriteErr(err)
	}
	if tag.RowsAffected() == 0 {
		return r.missingOrStale(ctx, c.ID)
	}
	return nil
}

func (r *CompanyRepo) Delete(ctx context.Context, id uuid.UUID, expectedVersion int64) error {
	tag, err := conn(ctx, r.pool).Exec(ctx, `DELETE FROM companies WHERE id = $1 AND version = $2`, id, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete company: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.missingOrStale(ctx, id)
	}
	return nil
}

func (r *CompanyRepo) missingOrStale(ctx context.Context, id uuid.UUID) error {
	var exists bool
	if err := conn(ctx, r.pool).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM companies WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("check company exists: %w", err)
	}
	if !exists {
		return domain.ErrNotFound
	}
	return domain.ErrVersionMismatch
}

func mapWriteErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == nameUniqConstraint {
		return domain.ErrNameTaken
	}
	return fmt.Errorf("write company: %w", err)
}
