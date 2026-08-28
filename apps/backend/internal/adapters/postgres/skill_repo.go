package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"

	"github.com/fauzanebd/argentum/internal/domain"
)

// SkillRepo stores the tenant's written procedures (T-K1).
//
// Every method takes the company id beside the row id, MCPServerRepo's rule for
// MCPServerRepo's reason: these are bare uuids in an admin URL, and the rows
// are the tenant's own prose about how their business works.
type SkillRepo struct{ db *sql.DB }

// NewSkillRepo builds the repository over the control database.
func NewSkillRepo(db *sql.DB) *SkillRepo { return &SkillRepo{db: db} }

const skillColumns = `id, company_id, name, when_to_use, body, enabled, source,
	COALESCE(created_by::text, ''), COALESCE(updated_by::text, ''), created_at, updated_at`

func scanSkill(row interface {
	Scan(dest ...interface{}) error
}) (*domain.Skill, error) {
	s := &domain.Skill{}
	if err := row.Scan(
		&s.ID, &s.CompanyID, &s.Name, &s.WhenToUse, &s.Body, &s.Enabled, &s.Source,
		&s.CreatedBy, &s.UpdatedBy, &s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return s, nil
}

// nullableUUID turns an empty id into a NULL rather than into the error
// Postgres gives for `”::uuid`. Both audit columns are optional: a skill
// shipped in `config/skills/` has no author, and a member who leaves takes
// their id out of these columns without taking the procedure.
func nullableUUID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// Create writes a skill. A duplicate name in the same company leaves this layer
// as ErrAlreadyExists: `load_skill` resolves what the model read off the index,
// so two skills whose names differ only in case is a tool call with no correct
// answer, and 069's unique index is what makes that unreachable.
func (r *SkillRepo) Create(ctx context.Context, s *domain.Skill) error {
	const q = `
		INSERT INTO skills (company_id, name, when_to_use, body, enabled, source, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		RETURNING id, created_at, updated_at`
	err := r.db.QueryRowContext(ctx, q,
		s.CompanyID, s.Name, s.WhenToUse, s.Body, s.Enabled, s.Source, nullableUUID(s.CreatedBy),
	).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
	if err != nil && uniqueViolation(err) {
		return domain.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("insert skill: %w", err)
	}
	s.UpdatedBy = s.CreatedBy
	return nil
}

func (r *SkillRepo) GetByID(ctx context.Context, companyID, id string) (*domain.Skill, error) {
	q := `SELECT ` + skillColumns + ` FROM skills WHERE company_id = $1 AND id = $2`
	s, err := scanSkill(r.db.QueryRowContext(ctx, q, companyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get skill: %w", err)
	}
	return s, nil
}

// GetByName resolves the name the model read off the index.
//
// `lower(name)` rather than `name`, matching 069's unique index so the lookup
// uses it: the index is prose in a prompt, and a model that retypes "Weekly
// Sales Report" as "weekly sales report" has named the same procedure. A
// case-sensitive miss here would be a refusal the model cannot understand,
// because the name it was given is right there in its own context.
func (r *SkillRepo) GetByName(ctx context.Context, companyID, name string) (*domain.Skill, error) {
	q := `SELECT ` + skillColumns + ` FROM skills WHERE company_id = $1 AND lower(name) = lower($2)`
	s, err := scanSkill(r.db.QueryRowContext(ctx, q, companyID, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get skill by name: %w", err)
	}
	return s, nil
}

func (r *SkillRepo) ListByCompany(ctx context.Context, companyID string) ([]*domain.Skill, error) {
	q := `SELECT ` + skillColumns + ` FROM skills WHERE company_id = $1 ORDER BY lower(name)`
	return r.querySkills(ctx, q, companyID)
}

// ListEnabledForIndex returns what T-K3 composes, in the order T-K3 truncates.
//
// Ordered by `lower(name)` rather than by creation date, and the reason is the
// truncation rather than tidiness: a tenant over either bound must lose the
// *same* skills every turn. An order that moved would change the cached prefix
// from turn to turn and make "why did it stop using that procedure" unanswerable.
func (r *SkillRepo) ListEnabledForIndex(ctx context.Context, companyID string) ([]*domain.Skill, error) {
	q := `SELECT ` + skillColumns + ` FROM skills WHERE company_id = $1 AND enabled ORDER BY lower(name)`
	return r.querySkills(ctx, q, companyID)
}

func (r *SkillRepo) querySkills(ctx context.Context, q string, args ...any) ([]*domain.Skill, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()

	out := []*domain.Skill{}
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, fmt.Errorf("scan skill: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Update rewrites the editable fields. `company_id` is in the WHERE rather than
// the SET: an update that could move a skill between tenants is one typo away
// from handing a procedure to somebody else's agent.
//
// **The vector is cleared when — and only when — the text it describes moved**
// (T-K5), and the comparison is against the stored row rather than against
// anything the caller remembered. An edit that fixes a typo in the body leaves
// the ranking alone; an edit that rewrites `when_to_use` invalidates a vector
// that now points at a sentence nobody wrote. Doing this in the same statement
// is what makes "stale vector" a state this table cannot be in, rather than a
// state a service is trusted to avoid.
func (r *SkillRepo) Update(ctx context.Context, s *domain.Skill) error {
	const q = `
		UPDATE skills
		SET name = $1, when_to_use = $2, body = $3, enabled = $4, updated_by = $5, updated_at = now(),
		    embedding       = CASE WHEN name = $1 AND when_to_use = $2 THEN embedding       ELSE NULL END,
		    embedding_model = CASE WHEN name = $1 AND when_to_use = $2 THEN embedding_model ELSE NULL END
		WHERE company_id = $6 AND id = $7`
	res, err := r.db.ExecContext(ctx, q,
		s.Name, s.WhenToUse, s.Body, s.Enabled, nullableUUID(s.UpdatedBy), s.CompanyID, s.ID)
	if err != nil && uniqueViolation(err) {
		return domain.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("update skill: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update skill rows: %w", err)
	}
	if n == 0 {
		// Either it does not exist or it belongs to somebody else, and this
		// layer deliberately cannot tell the caller which.
		return domain.ErrNotFound
	}
	return nil
}

func (r *SkillRepo) Delete(ctx context.Context, companyID, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM skills WHERE company_id = $1 AND id = $2`, companyID, id)
	if err != nil {
		return fmt.Errorf("delete skill: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete skill rows: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListEnabledRankedForIndex is T-K5: the same set ListEnabledForIndex returns,
// ordered by how close each skill's index line is to this turn's question.
//
// **Three sort keys, and the order between them is the design.** Rows with a
// vector come first, ordered by cosine distance; rows without one follow,
// ordered by `lower(name)`. So a company that has never been embedded gets
// ListEnabledForIndex's answer exactly, a company mid-backfill gets its ranked
// rows promoted and the rest in the old stable order, and nobody gets a
// silently different result depending on how far a background job has got.
//
// Cosine distance (`<=>`), matching QueryExampleRepo and TableEmbeddingRepo.
//
// No `k`: the caller is about to apply T-K3's two bounds, and a limit here
// would be a third bound in a third place. What this returns is an order, not a
// selection.
func (r *SkillRepo) ListEnabledRankedForIndex(
	ctx context.Context, companyID string, queryVec []float32,
) ([]*domain.Skill, error) {
	if len(queryVec) == 0 {
		return r.ListEnabledForIndex(ctx, companyID)
	}
	q := `SELECT ` + skillColumns + `
		FROM skills
		WHERE company_id = $1 AND enabled
		ORDER BY (embedding IS NULL), embedding <=> $2, lower(name)`
	return r.querySkills(ctx, q, companyID, pgvector.NewVector(queryVec))
}

// ListUnembedded returns the enabled skills with no vector, which is what the
// backfill works through.
//
// Enabled only: a disabled skill is not in any index, so embedding it would be
// paying for a ranking nobody will read. It gets its vector if and when it is
// switched back on and the company is over the bound.
func (r *SkillRepo) ListUnembedded(ctx context.Context, companyID string) ([]*domain.Skill, error) {
	q := `SELECT ` + skillColumns + `
		FROM skills WHERE company_id = $1 AND enabled AND embedding IS NULL ORDER BY lower(name)`
	return r.querySkills(ctx, q, companyID)
}

// SetEmbedding stores one skill's vector.
//
// **It does not touch `updated_at` or `updated_by`**, and that is the reason it
// is not part of Update. A backfill is not an edit: a tenant looking at who
// last changed a procedure must not be shown a vector job, and a support
// question about when a procedure changed must not be answered with the day
// somebody rotated the embedding model.
//
// **The write is conditional on the text still being what was embedded**, which
// is why this takes the skill rather than its id. The backfill reads a row,
// embeds it over a network call, and writes back; an admin who edits the
// trigger sentence inside that window would otherwise be handed a vector for
// the sentence they just replaced — a stale ranking with no way to tell it is
// stale. Losing that write costs one embedding call and the next backfill
// picks the row up again, so the safe direction is obvious.
func (r *SkillRepo) SetEmbedding(
	ctx context.Context, companyID string, s *domain.Skill, vec []float32, model string,
) error {
	if s == nil || len(vec) == 0 {
		return fmt.Errorf("set skill embedding: empty vector")
	}
	const q = `
		UPDATE skills SET embedding = $1, embedding_model = $2
		WHERE company_id = $3 AND id = $4 AND name = $5 AND when_to_use = $6`
	res, err := r.db.ExecContext(ctx, q, pgvector.NewVector(vec), model, companyID, s.ID, s.Name, s.WhenToUse)
	if err != nil {
		return fmt.Errorf("set skill embedding: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set skill embedding rows: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *SkillRepo) CountByCompany(ctx context.Context, companyID string) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM skills WHERE company_id = $1`, companyID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count skills: %w", err)
	}
	return n, nil
}

// SetAgentBinding replaces one agent's binding, in a transaction for
// replaceMCPServers' reason: a half-applied save leaves an agent bound to
// something the admin removed.
//
// Both statements re-check the company. The DELETE joins `agents` so an agent
// id belonging to another tenant clears nothing, and the INSERT joins `skills`
// so a skill id belonging to another tenant binds nothing — the service checks
// both for a good error message, and this is the layer where being wrong would
// put one company's procedures in another company's prompt.
func (r *SkillRepo) SetAgentBinding(ctx context.Context, companyID, agentID string, skillIDs []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin skill binding: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // committed below; this is the error path

	const del = `
		DELETE FROM agent_skills
		WHERE agent_id = (SELECT id FROM agents WHERE id = $1 AND company_id = $2)`
	if _, err := tx.ExecContext(ctx, del, agentID, companyID); err != nil {
		return fmt.Errorf("clear agent skills: %w", err)
	}
	if len(skillIDs) > 0 {
		const ins = `
			INSERT INTO agent_skills (agent_id, skill_id)
			SELECT a.id, s.id
			FROM agents a, skills s
			WHERE a.id = $1 AND a.company_id = $3
			  AND s.id = ANY($2::uuid[]) AND s.company_id = $3
			ON CONFLICT DO NOTHING`
		if _, err := tx.ExecContext(ctx, ins, agentID, pq.Array(skillIDs), companyID); err != nil {
			return fmt.Errorf("set agent skills: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit skill binding: %w", err)
	}
	return nil
}

// AgentBinding returns one agent's bound skill ids, empty for an agent nobody
// has bound — which means *every enabled company skill*, not none. The caller
// that forgets which way round this is has domain.Agent.AllowsSkill to read.
func (r *SkillRepo) AgentBinding(ctx context.Context, companyID, agentID string) ([]string, error) {
	const q = `
		SELECT b.skill_id::text
		FROM agent_skills b
		JOIN agents a ON a.id = b.agent_id
		WHERE b.agent_id = $1 AND a.company_id = $2
		ORDER BY b.skill_id`
	rows, err := r.db.QueryContext(ctx, q, agentID, companyID)
	if err != nil {
		return nil, fmt.Errorf("list agent skills: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan agent skill: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
