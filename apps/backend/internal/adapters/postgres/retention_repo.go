package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// RetentionRepo performs the bulk deletes behind T-H6's purge and erasure.
//
// **`messages` has no `company_id`.** It is scoped through
// `conversation_threads`, which is why every statement here carries an
// `EXISTS`/`IN` over that table rather than a WHERE on the message row. The
// house rule — every tenant-scoped query names `company_id` — is met by the
// subquery, and there is no statement in this file that could be run without
// one: a `DELETE FROM messages` with the tenant predicate accidentally omitted
// would empty the table for every customer at once, so the predicate is never
// optional and never assembled from a variable.
type RetentionRepo struct {
	db *sql.DB
}

// NewRetentionRepo builds the repository over the control database.
func NewRetentionRepo(db *sql.DB) *RetentionRepo { return &RetentionRepo{db: db} }

// PurgeCompanyMessages deletes one company's expired messages, then the threads
// left empty by that delete which are themselves past the window.
//
// One transaction, because the second statement's meaning depends on the first
// having landed: "threads with no messages" is only the right set once the
// expired messages are gone. A reader between the two would see a thread that
// looks empty and is about to be deleted, which is fine, and a crash between
// them would leave husks the next tick removes, which is also fine — but only
// if both statements agree about what "empty" means.
func (r *RetentionRepo) PurgeCompanyMessages(ctx context.Context, companyID string, before time.Time) (int, int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin retention purge: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // committed below; this is the error path

	const delMessages = `
		DELETE FROM messages m
		USING conversation_threads t
		WHERE m.thread_id = t.id
		  AND t.company_id = $1
		  AND m.created_at < $2`
	res, err := tx.ExecContext(ctx, delMessages, companyID, before)
	if err != nil {
		return 0, 0, fmt.Errorf("purge messages: %w", err)
	}
	messages, err := res.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("purge messages rows: %w", err)
	}

	// `last_message_at` rather than `created_at`: a thread opened last year and
	// used yesterday is not expired, and its own creation date says the
	// opposite. NOT EXISTS rather than a LEFT JOIN count, because the index on
	// (thread_id, created_at) makes the existence check a single probe per
	// thread.
	const delThreads = `
		DELETE FROM conversation_threads t
		WHERE t.company_id = $1
		  AND t.last_message_at < $2
		  AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.thread_id = t.id)`
	res, err = tx.ExecContext(ctx, delThreads, companyID, before)
	if err != nil {
		return 0, 0, fmt.Errorf("purge empty threads: %w", err)
	}
	threads, err := res.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("purge threads rows: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit retention purge: %w", err)
	}
	return int(threads), int(messages), nil
}

// EraseCompanyConversations deletes every thread a company has. Messages go
// with them through 002's `ON DELETE CASCADE`, and so do the other rows that
// chose to cascade from a thread — saved dashboards' thread links, documents,
// scheduled tasks, message feedback, harvested query examples.
//
// **`agent_actions` is untouched, and it is untouched by construction rather
// than by this query being careful.** Migration 023 gave it no foreign key on
// `thread_id` or `message_id` for exactly this reason: "a CASCADE would let a
// user erase the record of what the agent did in a thread by deleting the
// thread". Erasure is that same delete with a wider WHERE, so the property it
// needs was already paid for. `usage_events` keeps its rows too — its FKs are
// SET NULL — which is right: what a tenant was billed is not their personal
// data and is the one record a billing dispute needs.
func (r *RetentionRepo) EraseCompanyConversations(ctx context.Context, companyID string) (int, int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin erasure: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // committed below; this is the error path

	// Counted before the delete rather than after, because CASCADE removes the
	// messages without reporting how many — and the written record's whole
	// value is that the number in it is true.
	const countMessages = `
		SELECT count(*) FROM messages m
		JOIN conversation_threads t ON t.id = m.thread_id
		WHERE t.company_id = $1`
	var messages int
	if err := tx.QueryRowContext(ctx, countMessages, companyID).Scan(&messages); err != nil {
		return 0, 0, fmt.Errorf("count messages for erasure: %w", err)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM conversation_threads WHERE company_id = $1`, companyID)
	if err != nil {
		return 0, 0, fmt.Errorf("erase threads: %w", err)
	}
	threads, err := res.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("erase threads rows: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit erasure: %w", err)
	}
	return int(threads), messages, nil
}

// HasExpired answers whether one tenant has anything for tonight's tick.
//
// Two shapes count as expired, and they are the same two the purge deletes: a
// message older than the cutoff, and a thread that is both past the cutoff and
// already empty. Missing the second would leave husks that never get collected
// — the tick would skip the tenant forever on the grounds that no *message* is
// expired, which is how a bug fixing one log line creates a slower one.
//
// EXISTS over a UNION ALL of the two, LIMIT 1: Postgres stops at the first row
// either arm produces, so the ordinary answer (nothing expired) costs two
// index probes and no scan.
func (r *RetentionRepo) HasExpired(ctx context.Context, companyID string, before time.Time) (bool, error) {
	const q = `
		SELECT EXISTS (
			SELECT 1
			FROM messages m
			JOIN conversation_threads t ON t.id = m.thread_id
			WHERE t.company_id = $1
			  AND m.created_at < $2
			UNION ALL
			SELECT 1
			FROM conversation_threads t
			WHERE t.company_id = $1
			  AND t.last_message_at < $2
			  AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.thread_id = t.id)
			LIMIT 1
		)`
	var found bool
	if err := r.db.QueryRowContext(ctx, q, companyID, before).Scan(&found); err != nil {
		return false, fmt.Errorf("check expired conversations: %w", err)
	}
	return found, nil
}

// CompaniesWithRetention returns only the tenants that opted in. A deployment
// where nobody has set a window does no per-company work at all, which is what
// makes a nightly tick free on the deployments this ships to first.
func (r *RetentionRepo) CompaniesWithRetention(ctx context.Context) ([]domain.CompanyRetention, error) {
	const q = `
		SELECT id, message_retention_days
		FROM companies
		WHERE message_retention_days > 0
		ORDER BY id`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list companies with retention: %w", err)
	}
	defer rows.Close()

	var out []domain.CompanyRetention
	for rows.Next() {
		var c domain.CompanyRetention
		if err := rows.Scan(&c.CompanyID, &c.Days); err != nil {
			return nil, fmt.Errorf("scan company retention: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ExportCompanyConversations streams the whole transcript history, oldest
// first, calling fn per row.
//
// A callback rather than a returned slice: this is the route that exists so
// erasure is not the only exit, which means it is called by the tenants with
// the most to lose and therefore the most rows. Materialising a large tenant's
// entire history to serialise it is how an export endpoint takes the API down.
func (r *RetentionRepo) ExportCompanyConversations(ctx context.Context, companyID string, fn func(domain.ExportedMessage) error) error {
	const q = `
		SELECT t.id, t.title, t.channel, m.id, m.role, m.content, m.tool_calls, m.created_at
		FROM messages m
		JOIN conversation_threads t ON t.id = m.thread_id
		WHERE t.company_id = $1
		ORDER BY t.created_at, m.created_at, m.id`
	rows, err := r.db.QueryContext(ctx, q, companyID)
	if err != nil {
		return fmt.Errorf("export conversations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var m domain.ExportedMessage
		var toolCalls []byte
		if err := rows.Scan(
			&m.ThreadID, &m.ThreadTitle, &m.Channel,
			&m.MessageID, &m.Role, &m.Content, &toolCalls, &m.CreatedAt,
		); err != nil {
			return fmt.Errorf("scan exported message: %w", err)
		}
		m.ToolCalls = toolCalls
		if err := fn(m); err != nil {
			return err
		}
	}
	return rows.Err()
}
