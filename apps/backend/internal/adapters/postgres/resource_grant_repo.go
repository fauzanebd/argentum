package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/domain"
)

// resourceTable is where a restrictable kind lives, and which typed column on
// resource_grants points at it (084).
//
// The statements in this file are assembled from this map with fmt.Sprintf, and
// it is the only thing ever formatted into them: identifiers and expressions per
// kind, fixed at compile time and keyed by a closed vocabulary, never anything
// from a request. Every value is still a bound parameter. The alternative is four
// hand-written copies of each of five statements, and twenty near-identical
// strings is how one of them ends up missing its company_id.
type resourceTable struct {
	table, column string
	// name is the expression, over the table aliased `r`, that says what a
	// resource is called (T-Z5) — its label and nothing from inside it.
	name string
	// closeOnRestrict, when set, runs in SetAccessMode's transaction after a
	// flip to restricted, with $1 the company and $2 the resource, and its row
	// count is reported. It is what a restriction has to take away that is not a
	// grant: the doors onto the resource that have no person behind them.
	closeOnRestrict string
}

var resourceTables = map[domain.ResourceKind]resourceTable{
	domain.ResourceKindAgent: {table: "agents", column: "agent_id", name: "r.name"},
	domain.ResourceKindDashboard: {
		table: "dashboards", column: "dashboard_id", name: "r.title",
		// Live links only. An expired one can never open again, and revoking it
		// would rewrite the record of a link that simply ran out.
		closeOnRestrict: `
			UPDATE dashboard_shares SET revoked_at = now()
			 WHERE company_id = $1 AND dashboard_id = $2
			   AND revoked_at IS NULL AND expires_at > now()`,
	},
	// A source's label is optional (001), and a nameless row reads as its engine,
	// which is what the sources page shows for one too.
	domain.ResourceKindConnection: {table: "db_connections", column: "connection_id", name: "COALESCE(NULLIF(r.label, ''), r.db_type)"},
	domain.ResourceKindDocument:   {table: "source_documents", column: "document_id", name: "r.filename"},
}

func tableFor(kind domain.ResourceKind) (resourceTable, error) {
	t, ok := resourceTables[kind]
	if !ok {
		return resourceTable{}, fmt.Errorf("%w: %q is not a resource kind", domain.ErrInvalidInput, kind)
	}
	return t, nil
}

// ResourceGrantRepo persists access modes and resource grants (T-Z2), and is
// the loader internal/authz decides against.
type ResourceGrantRepo struct{ db *sql.DB }

// NewResourceGrantRepo wires the control database.
func NewResourceGrantRepo(db *sql.DB) *ResourceGrantRepo { return &ResourceGrantRepo{db: db} }

// LoadAccess answers for every id in one statement — the property authz.Visible
// promises its callers.
//
// A malformed id anywhere in the batch makes Postgres refuse the whole array
// (22P02), and that is answered as "none of these exist" rather than as an
// error. Ids that reach a batch come from this database and are well-formed; a
// malformed one arrives from a URL, alone, and not-found is the right answer
// for it. Refusing everything is the safe side of the one case where that
// reading is too broad.
func (r *ResourceGrantRepo) LoadAccess(ctx context.Context, companyID, userID string, kind domain.ResourceKind, ids []string) (map[string]domain.ResourceAccess, error) {
	out := map[string]domain.ResourceAccess{}
	if len(ids) == 0 {
		return out, nil
	}
	t, err := tableFor(kind)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`
		SELECT r.id::text, r.access_mode,
		       EXISTS (
		           SELECT 1 FROM resource_grants g
		            WHERE g.company_id = r.company_id
		              AND g.user_id = NULLIF($2, '')::uuid
		              AND g.%[2]s = r.id
		       )
		  FROM %[1]s r
		 WHERE r.company_id = $1 AND r.id = ANY($3::uuid[])
	`, t.table, t.column)

	rows, err := r.db.QueryContext(ctx, q, companyID, userID, pq.Array(ids))
	if err != nil {
		if malformedID(err) {
			return out, nil
		}
		return nil, fmt.Errorf("load %s access: %w", kind, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id      string
			mode    string
			granted bool
		)
		if err := rows.Scan(&id, &mode, &granted); err != nil {
			return nil, fmt.Errorf("scan %s access: %w", kind, err)
		}
		out[id] = domain.ResourceAccess{Mode: domain.AccessMode(mode), Granted: granted}
	}
	if err := rows.Err(); err != nil {
		if malformedID(err) {
			return map[string]domain.ResourceAccess{}, nil
		}
		return nil, fmt.Errorf("load %s access: %w", kind, err)
	}
	return out, nil
}

// View returns one resource's mode and every grant on it, from one statement,
// so the mode and the list cannot be read at two different moments.
func (r *ResourceGrantRepo) View(ctx context.Context, companyID string, kind domain.ResourceKind, id string) (*domain.ResourceAccessView, error) {
	t, err := tableFor(kind)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`
		SELECT %[3]s, r.access_mode, g.user_id::text, g.granted_by::text, g.granted_at
		  FROM %[1]s r
		  LEFT JOIN resource_grants g
		         ON g.company_id = r.company_id AND g.%[2]s = r.id
		 WHERE r.id = $2 AND r.company_id = $1
		 ORDER BY g.granted_at, g.user_id
	`, t.table, t.column, t.name)

	rows, err := r.db.QueryContext(ctx, q, companyID, id)
	if err != nil {
		if malformedID(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("view %s access: %w", kind, err)
	}
	defer rows.Close()

	var view *domain.ResourceAccessView
	for rows.Next() {
		var (
			name, mode string
			userID     sql.NullString
			grantedBy  sql.NullString
			grantedAt  sql.NullTime
		)
		if err := rows.Scan(&name, &mode, &userID, &grantedBy, &grantedAt); err != nil {
			return nil, fmt.Errorf("scan %s access: %w", kind, err)
		}
		if view == nil {
			view = &domain.ResourceAccessView{
				Kind:       kind,
				ResourceID: id,
				Name:       name,
				AccessMode: domain.AccessMode(mode),
				Grants:     []domain.ResourceGrant{},
			}
		}
		if !userID.Valid {
			continue // the LEFT JOIN's row for a resource nobody is granted
		}
		view.Grants = append(view.Grants, domain.ResourceGrant{
			UserID:     userID.String,
			Kind:       kind,
			ResourceID: id,
			GrantedBy:  grantedBy.String,
			GrantedAt:  grantedAt.Time,
		})
	}
	if err := rows.Err(); err != nil {
		if malformedID(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("view %s access: %w", kind, err)
	}
	if view == nil {
		return nil, domain.ErrNotFound
	}
	return view, nil
}

// ListViews is View over every resource of a kind in the company, from one
// statement (T-Z7).
//
// The access matrix answers two questions — who may reach this agent, and what
// may this person reach — and it draws both from this read. Two reads, one per
// direction, could be taken either side of a grant and show an admin a person
// in one list and not in the other; one read cannot disagree with itself.
//
// Every resource is in the answer, open ones and ones nobody holds included,
// because the matrix's columns are the resources and not the grants. Grouping
// is by first appearance, which ORDER BY r.id keeps contiguous.
func (r *ResourceGrantRepo) ListViews(ctx context.Context, companyID string, kind domain.ResourceKind) ([]domain.ResourceAccessView, error) {
	t, err := tableFor(kind)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`
		SELECT r.id::text, %[3]s, r.access_mode, g.user_id::text, g.granted_by::text, g.granted_at
		  FROM %[1]s r
		  LEFT JOIN resource_grants g
		         ON g.company_id = r.company_id AND g.%[2]s = r.id
		 WHERE r.company_id = $1
		 ORDER BY r.id, g.granted_at, g.user_id
	`, t.table, t.column, t.name)

	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return nil, fmt.Errorf("list %s access: %w", kind, err)
	}
	defer rows.Close()

	out := []domain.ResourceAccessView{}
	at := map[string]int{}
	for rows.Next() {
		var (
			id, name, mode string
			userID         sql.NullString
			grantedBy      sql.NullString
			grantedAt      sql.NullTime
		)
		if err := rows.Scan(&id, &name, &mode, &userID, &grantedBy, &grantedAt); err != nil {
			return nil, fmt.Errorf("scan %s access: %w", kind, err)
		}
		i, seen := at[id]
		if !seen {
			i = len(out)
			at[id] = i
			out = append(out, domain.ResourceAccessView{
				Kind:       kind,
				ResourceID: id,
				Name:       name,
				AccessMode: domain.AccessMode(mode),
				Grants:     []domain.ResourceGrant{},
			})
		}
		if !userID.Valid {
			continue // the LEFT JOIN's row for a resource nobody is granted
		}
		out[i].Grants = append(out[i].Grants, domain.ResourceGrant{
			UserID:     userID.String,
			Kind:       kind,
			ResourceID: id,
			GrantedBy:  grantedBy.String,
			GrantedAt:  grantedAt.Time,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list %s access: %w", kind, err)
	}
	return out, nil
}

// ListForUser returns every grant a user holds, across kinds. The LEFT JOIN
// from users is CapabilityRepo.ListForUser's: no row means not a user of this
// company, one row of NULLs means holds nothing.
func (r *ResourceGrantRepo) ListForUser(ctx context.Context, companyID, userID string) ([]domain.ResourceGrant, error) {
	const q = `
		SELECT g.resource_kind,
		       COALESCE(g.agent_id, g.dashboard_id, g.connection_id, g.document_id)::text,
		       g.granted_by::text, g.granted_at
		  FROM users u
		  LEFT JOIN resource_grants g
		         ON g.company_id = u.company_id AND g.user_id = u.id
		 WHERE u.id = $2 AND u.company_id = $1
		 ORDER BY g.resource_kind, g.granted_at
	`
	rows, err := r.db.QueryContext(ctx, q, companyID, userID)
	if err != nil {
		if malformedID(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("list resource grants: %w", err)
	}
	defer rows.Close()

	found := false
	out := []domain.ResourceGrant{}
	for rows.Next() {
		found = true
		var (
			kind       sql.NullString
			resourceID sql.NullString
			grantedBy  sql.NullString
			grantedAt  sql.NullTime
		)
		if err := rows.Scan(&kind, &resourceID, &grantedBy, &grantedAt); err != nil {
			return nil, fmt.Errorf("scan resource grant: %w", err)
		}
		if !kind.Valid {
			continue // the LEFT JOIN's row for a user who holds nothing
		}
		out = append(out, domain.ResourceGrant{
			UserID:     userID,
			Kind:       domain.ResourceKind(kind.String),
			ResourceID: resourceID.String,
			GrantedBy:  grantedBy.String,
			GrantedAt:  grantedAt.Time,
		})
	}
	if err := rows.Err(); err != nil {
		if malformedID(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("list resource grants: %w", err)
	}
	if !found {
		return nil, domain.ErrNotFound
	}
	return out, nil
}

// SetAccessMode writes the mode, and on a flip to restricted closes whatever the
// kind's closeOnRestrict names, in one transaction. updated_at is left alone on
// purpose: on a dashboard or an agent it means "the definition changed", and who
// may open a thing is not a change to what the thing is.
//
// **The flip comes first, and that order is the property.** The UPDATE takes the
// resource's row lock, which DashboardShareRepo.Insert also asks for; the revoke
// is a later statement, so under READ COMMITTED it sees any link a mint holding
// that lock committed while the flip waited. Revoking first would miss exactly
// that link.
func (r *ResourceGrantRepo) SetAccessMode(ctx context.Context, companyID string, kind domain.ResourceKind, id string, mode domain.AccessMode) (domain.AccessModeChange, error) {
	change := domain.AccessModeChange{AccessMode: mode}
	t, err := tableFor(kind)
	if err != nil {
		return change, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return change, fmt.Errorf("set %s access mode: %w", kind, err)
	}
	defer func() { _ = tx.Rollback() }()

	q := fmt.Sprintf(`UPDATE %s SET access_mode = $3 WHERE id = $2 AND company_id = $1`, t.table)
	res, err := tx.ExecContext(ctx, q, companyID, id, string(mode))
	if err != nil {
		if malformedID(err) {
			return change, domain.ErrNotFound
		}
		return change, fmt.Errorf("set %s access mode: %w", kind, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return change, fmt.Errorf("set %s access mode: %w", kind, err)
	}
	if n == 0 {
		return change, domain.ErrNotFound
	}

	if mode == domain.AccessModeRestricted && t.closeOnRestrict != "" {
		res, err := tx.ExecContext(ctx, t.closeOnRestrict, companyID, id)
		if err != nil {
			return change, fmt.Errorf("close %s on restrict: %w", kind, err)
		}
		closed, err := res.RowsAffected()
		if err != nil {
			return change, fmt.Errorf("close %s on restrict: %w", kind, err)
		}
		change.RevokedShares = int(closed)
	}
	if err := tx.Commit(); err != nil {
		return change, fmt.Errorf("set %s access mode: %w", kind, err)
	}
	return change, nil
}

// Grant writes the grant if both the user and the resource belong to the
// company, in one statement and idempotently.
//
// `target` is the pair, found only when both rows carry this company_id — a
// foreign key proves each exists, and neither proves it is here. The data-
// modifying CTE runs whether or not the outer SELECT reads it, and the count
// of `target` is the not-found answer.
func (r *ResourceGrantRepo) Grant(ctx context.Context, companyID, userID string, kind domain.ResourceKind, id, grantedBy string) error {
	t, err := tableFor(kind)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`
		WITH target AS (
			SELECT u.id AS user_id, r.id AS resource_id
			  FROM users u
			  JOIN %[1]s r ON r.company_id = u.company_id
			 WHERE u.id = $2 AND u.company_id = $1 AND r.id = $3
		), granted AS (
			INSERT INTO resource_grants (company_id, user_id, resource_kind, %[2]s, granted_by)
			SELECT $1::uuid, user_id, $4::text, resource_id, NULLIF($5, '')::uuid FROM target
			ON CONFLICT (company_id, user_id, %[2]s) WHERE %[2]s IS NOT NULL DO NOTHING
		)
		SELECT count(*) FROM target
	`, t.table, t.column)
	return countTarget(ctx, r.db, "grant "+string(kind), q, companyID, userID, id, string(kind), grantedBy)
}

// Revoke deletes the grant if it exists. Grant's `target` again, so a user or
// resource of another company is a not-found rather than a successful delete of
// nothing.
func (r *ResourceGrantRepo) Revoke(ctx context.Context, companyID, userID string, kind domain.ResourceKind, id string) error {
	t, err := tableFor(kind)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`
		WITH target AS (
			SELECT u.id AS user_id, r.id AS resource_id
			  FROM users u
			  JOIN %[1]s r ON r.company_id = u.company_id
			 WHERE u.id = $2 AND u.company_id = $1 AND r.id = $3
		), revoked AS (
			DELETE FROM resource_grants
			 WHERE company_id = $1 AND user_id = $2 AND %[2]s = $3
		)
		SELECT count(*) FROM target
	`, t.table, t.column)
	return countTarget(ctx, r.db, "revoke "+string(kind), q, companyID, userID, id)
}
