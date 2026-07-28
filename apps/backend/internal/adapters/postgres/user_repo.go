package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

type UserRepo struct{ db *sql.DB }

func NewUserRepo(db *sql.DB) *UserRepo { return &UserRepo{db: db} }

// userColumns is shared by every read so a new column cannot be added to one
// query and forgotten in another; scanUser is its counterpart.
const userColumns = `id, company_id, email, password_hash, role, created_at, activated_at, deactivated_at`

func (r *UserRepo) Create(ctx context.Context, u *domain.User) error {
	const q = `
		INSERT INTO users (company_id, email, password_hash, role, activated_at)
		VALUES ($1, $2, $3, $4, now())
		RETURNING id, created_at, activated_at
	`
	if err := r.db.QueryRowContext(ctx, q,
		u.CompanyID, u.Email, u.PasswordHash, string(u.Role),
	).Scan(&u.ID, &u.CreatedAt, &u.ActivatedAt); err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

// CreatePending inserts a user who cannot log in yet: activated_at is NULL and
// the password hash is the empty string, which VerifyPassword rejects on its
// own even before the active check runs.
func (r *UserRepo) CreatePending(ctx context.Context, u *domain.User) error {
	const q = `
		INSERT INTO users (company_id, email, password_hash, role, activated_at)
		VALUES ($1, $2, '', $3, NULL)
		RETURNING id, created_at
	`
	if err := r.db.QueryRowContext(ctx, q,
		u.CompanyID, u.Email, string(u.Role),
	).Scan(&u.ID, &u.CreatedAt); err != nil {
		return fmt.Errorf("insert pending user: %w", err)
	}
	u.PasswordHash = ""
	u.ActivatedAt = nil
	return nil
}

func (r *UserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	return r.scanOne(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	return r.scanOne(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, email)
}

func (r *UserRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.User, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE company_id = $1 ORDER BY created_at`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Activate is guarded by `activated_at IS NULL` rather than a read-then-write,
// so two accepts of the same invite cannot both succeed: the second updates
// zero rows and gets ErrNotFound.
func (r *UserRepo) Activate(ctx context.Context, id, passwordHash string, at time.Time) error {
	const q = `
		UPDATE users
		   SET password_hash = $2, activated_at = $3
		 WHERE id = $1 AND activated_at IS NULL AND deactivated_at IS NULL
	`
	return r.exactlyOne(ctx, q, id, passwordHash, at)
}

func (r *UserRepo) UpdateRole(ctx context.Context, companyID, id string, role domain.Role) error {
	const q = `UPDATE users SET role = $3 WHERE id = $1 AND company_id = $2`
	return r.exactlyOne(ctx, q, id, companyID, string(role))
}

func (r *UserRepo) Deactivate(ctx context.Context, companyID, id string, at time.Time) error {
	const q = `
		UPDATE users SET deactivated_at = $3
		 WHERE id = $1 AND company_id = $2 AND deactivated_at IS NULL
	`
	return r.exactlyOne(ctx, q, id, companyID, at)
}

func (r *UserRepo) Delete(ctx context.Context, companyID, id string) error {
	const q = `DELETE FROM users WHERE id = $1 AND company_id = $2`
	return r.exactlyOne(ctx, q, id, companyID)
}

func (r *UserRepo) CountActiveAdmins(ctx context.Context, companyID string) (int, error) {
	const q = `
		SELECT count(*) FROM users
		 WHERE company_id = $1 AND role = 'admin'
		   AND activated_at IS NOT NULL AND deactivated_at IS NULL
	`
	var n int
	if err := r.db.QueryRowContext(ctx, q, companyID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// exactlyOne runs a statement that must touch one row and maps "touched none"
// to ErrNotFound. Every caller uses it so that a company-scoped WHERE clause
// turns a cross-tenant write into a 404 rather than a vacuous success.
func (r *UserRepo) exactlyOne(ctx context.Context, q string, args ...any) error {
	res, err := r.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *UserRepo) scanOne(ctx context.Context, q string, arg any) (*domain.User, error) {
	u, err := scanUser(r.db.QueryRowContext(ctx, q, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// rowScanner (declared in thread_repo.go) is satisfied by both *sql.Row and
// *sql.Rows, so one scan function serves the single-row and list queries.
func scanUser(s rowScanner) (*domain.User, error) {
	u := &domain.User{}
	var role string
	var activated, deactivated sql.NullTime
	if err := s.Scan(&u.ID, &u.CompanyID, &u.Email, &u.PasswordHash, &role,
		&u.CreatedAt, &activated, &deactivated); err != nil {
		return nil, err
	}
	u.Role = domain.Role(role)
	if activated.Valid {
		t := activated.Time
		u.ActivatedAt = &t
	}
	if deactivated.Valid {
		t := deactivated.Time
		u.DeactivatedAt = &t
	}
	return u, nil
}
