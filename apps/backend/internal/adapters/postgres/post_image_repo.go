package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/domain"
)

// PostImageRepo persists the pictures a tenant uploaded for posts (T-G12).
type PostImageRepo struct{ db *sql.DB }

func NewPostImageRepo(db *sql.DB) *PostImageRepo { return &PostImageRepo{db: db} }

const postImageColumns = `
	id, company_id, name, alt, storage_key, width, height, byte_size,
	uploaded_by, created_at, updated_at`

func scanPostImage(s interface{ Scan(...any) error }) (*domain.PostImage, error) {
	img := &domain.PostImage{}
	var uploadedBy sql.NullString
	if err := s.Scan(
		&img.ID, &img.CompanyID, &img.Name, &img.Alt, &img.StorageKey,
		&img.Width, &img.Height, &img.ByteSize, &uploadedBy,
		&img.CreatedAt, &img.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if uploadedBy.Valid {
		img.UploadedBy = uploadedBy.String
	}
	return img, nil
}

// Insert writes the row. A second image with the same name in the same
// company comes back as ErrAlreadyExists rather than a driver error, because
// the name is the model's handle on the picture and the caller answers a
// collision with a sentence about renaming rather than with a 500.
func (r *PostImageRepo) Insert(ctx context.Context, img *domain.PostImage) error {
	const q = `
		INSERT INTO post_images
			(company_id, name, alt, storage_key, width, height, byte_size, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, '')::uuid)
		RETURNING id, created_at, updated_at`
	err := r.db.QueryRowContext(ctx, q,
		img.CompanyID, img.Name, img.Alt, img.StorageKey,
		img.Width, img.Height, img.ByteSize, img.UploadedBy,
	).Scan(&img.ID, &img.CreatedAt, &img.UpdatedAt)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code.Name() == "unique_violation" {
		return domain.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("insert post image: %w", err)
	}
	return nil
}

func (r *PostImageRepo) GetForCompany(ctx context.Context, companyID, id string) (*domain.PostImage, error) {
	q := `SELECT ` + postImageColumns + ` FROM post_images WHERE company_id = $1 AND id = $2`
	img, err := scanPostImage(r.db.QueryRowContext(ctx, q, companyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read post image: %w", err)
	}
	return img, nil
}

func (r *PostImageRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.PostImage, error) {
	q := `SELECT ` + postImageColumns + `
		FROM post_images WHERE company_id = $1 ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, fmt.Errorf("list post images: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []*domain.PostImage{}
	for rows.Next() {
		img, err := scanPostImage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan post image: %w", err)
		}
		out = append(out, img)
	}
	return out, rows.Err()
}

// FindByName resolves the name a model wrote.
//
// **Exact on the lower-cased name, and nothing cleverer.** A prefix or
// similarity match would let "jeruk" pick one of five citrus photographs and
// give a promotion the wrong product, which is a mistake nobody notices until
// it is public. A miss is a miss, and the caller says so.
func (r *PostImageRepo) FindByName(ctx context.Context, companyID, name string) (*domain.PostImage, error) {
	q := `SELECT ` + postImageColumns + `
		FROM post_images WHERE company_id = $1 AND lower(name) = lower($2)`
	img, err := scanPostImage(r.db.QueryRowContext(ctx, q, companyID, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find post image by name: %w", err)
	}
	return img, nil
}

// Delete removes the row. The object is left in the bucket by this method and
// removed by the service, which is the only caller that holds a store.
func (r *PostImageRepo) Delete(ctx context.Context, companyID, id string) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM post_images WHERE company_id = $1 AND id = $2`, companyID, id)
	if err != nil {
		return fmt.Errorf("delete post image: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
