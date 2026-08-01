package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

// SourceProfileRepo persists what one connected source looks like it is for
// (T-B2).
type SourceProfileRepo struct{ db *sql.DB }

func NewSourceProfileRepo(db *sql.DB) *SourceProfileRepo { return &SourceProfileRepo{db: db} }

const sourceProfileCols = `connection_id, company_id, industry, summary, entities,
	schema_fingerprint, model, inferred_at`

// GetByConnection returns one source's draft, or domain.ErrNotFound when it has
// never been inferred.
//
// company_id is in the WHERE clause beside the primary key, not because the id
// is guessable but because this is a tenant-scoped read and the isolation
// boundary has no exceptions: a caller that passes another company's connection
// id gets not-found, which is the same answer as a connection that does not
// exist and the only one that leaks nothing.
func (r *SourceProfileRepo) GetByConnection(ctx context.Context, companyID, connectionID string) (*domain.SourceProfile, error) {
	q := `SELECT ` + sourceProfileCols + ` FROM source_profiles
	      WHERE connection_id = $1 AND company_id = $2`
	row := r.db.QueryRowContext(ctx, q, connectionID, companyID)
	p, err := scanSourceProfile(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, domain.ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("get source profile: %w", err)
	}
	return p, nil
}

// ListByCompany returns every source draft the company has, default source
// first.
//
// The order is what the fold depends on: DraftFromSources takes the first
// non-empty industry it sees, and the source the tenant made default is the one
// whose answer should win. The join is to db_connections rather than a stored
// rank because "which source is default" changes without this table hearing
// about it.
func (r *SourceProfileRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.SourceProfile, error) {
	q := `SELECT ` + prefixedSourceProfileCols + ` FROM source_profiles sp
	      JOIN db_connections c ON c.id = sp.connection_id
	      WHERE sp.company_id = $1
	      ORDER BY c.is_default DESC, sp.inferred_at DESC`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, fmt.Errorf("list source profiles: %w", err)
	}
	defer rows.Close()

	var out []*domain.SourceProfile
	for rows.Next() {
		p, err := scanSourceProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("scan source profile: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source profiles: %w", err)
	}
	return out, nil
}

const prefixedSourceProfileCols = `sp.connection_id, sp.company_id, sp.industry, sp.summary,
	sp.entities, sp.schema_fingerprint, sp.model, sp.inferred_at`

// Upsert writes one source's draft, replacing whatever was there.
//
// A re-scan produces a whole new answer, not a patch: the schema it describes
// has changed, and merging the previous draft's entities into the new one would
// leave the tenant reviewing a list containing tables that were dropped last
// week.
func (r *SourceProfileRepo) Upsert(ctx context.Context, p *domain.SourceProfile) error {
	entities, err := json.Marshal(nonNilEntities(p.Entities))
	if err != nil {
		return fmt.Errorf("marshal source entities: %w", err)
	}
	const q = `
		INSERT INTO source_profiles (
			connection_id, company_id, industry, summary, entities,
			schema_fingerprint, model, inferred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (connection_id) DO UPDATE SET
			company_id = EXCLUDED.company_id,
			industry = EXCLUDED.industry,
			summary = EXCLUDED.summary,
			entities = EXCLUDED.entities,
			schema_fingerprint = EXCLUDED.schema_fingerprint,
			model = EXCLUDED.model,
			inferred_at = now()
		RETURNING inferred_at
	`
	if err := r.db.QueryRowContext(ctx, q,
		p.ConnectionID, p.CompanyID, p.Industry, p.Summary, entities,
		p.SchemaFingerprint, p.Model,
	).Scan(&p.InferredAt); err != nil {
		return fmt.Errorf("upsert source profile: %w", err)
	}
	return nil
}

// nonNilEntities keeps the column's JSON an array rather than `null`. The
// default is '[]' and a nil slice would marshal to something the column's
// readers then have to special-case.
func nonNilEntities(in []domain.SourceEntity) []domain.SourceEntity {
	if in == nil {
		return []domain.SourceEntity{}
	}
	return in
}

// scanSourceProfile reads one row through the package's rowScanner (declared in
// thread_repo.go), so the single-row and multi-row reads share one column list
// and cannot drift.
func scanSourceProfile(s rowScanner) (*domain.SourceProfile, error) {
	p := &domain.SourceProfile{}
	var entities []byte
	if err := s.Scan(
		&p.ConnectionID, &p.CompanyID, &p.Industry, &p.Summary, &entities,
		&p.SchemaFingerprint, &p.Model, &p.InferredAt,
	); err != nil {
		return nil, err
	}
	if len(entities) > 0 {
		if err := json.Unmarshal(entities, &p.Entities); err != nil {
			return nil, fmt.Errorf("unmarshal source entities: %w", err)
		}
	}
	return p, nil
}
