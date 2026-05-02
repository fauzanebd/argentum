package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

type UserRepo struct{ db *sql.DB }

func NewUserRepo(db *sql.DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) Create(ctx context.Context, u *domain.User) error {
	const q = `
		INSERT INTO users (company_id, email, password_hash, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at
	`
	if err := r.db.QueryRowContext(ctx, q,
		u.CompanyID, u.Email, u.PasswordHash, string(u.Role),
	).Scan(&u.ID, &u.CreatedAt); err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (r *UserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	const q = `SELECT id, company_id, email, password_hash, role, created_at FROM users WHERE id = $1`
	return r.scanOne(ctx, q, id)
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	const q = `SELECT id, company_id, email, password_hash, role, created_at FROM users WHERE email = $1`
	return r.scanOne(ctx, q, email)
}

func (r *UserRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.User, error) {
	const q = `SELECT id, company_id, email, password_hash, role, created_at FROM users WHERE company_id = $1 ORDER BY created_at`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.User
	for rows.Next() {
		u := &domain.User{}
		var role string
		if err := rows.Scan(&u.ID, &u.CompanyID, &u.Email, &u.PasswordHash, &role, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.Role = domain.Role(role)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *UserRepo) scanOne(ctx context.Context, q string, arg interface{}) (*domain.User, error) {
	u := &domain.User{}
	var role string
	err := r.db.QueryRowContext(ctx, q, arg).Scan(&u.ID, &u.CompanyID, &u.Email, &u.PasswordHash, &role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Role = domain.Role(role)
	return u, nil
}
