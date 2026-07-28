package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

type ThreadRepo struct{ db *sql.DB }

func NewThreadRepo(db *sql.DB) *ThreadRepo { return &ThreadRepo{db: db} }

const threadSelectCols = `id, company_id, channel, COALESCE(phone_number, ''), COALESCE(user_id::text, ''),
		COALESCE(discord_user_id, ''), COALESCE(lark_chat_id, ''), COALESCE(lark_thread_key, ''),
		COALESCE(lark_open_id, ''), COALESCE(api_user_ref, ''),
		title, summary, last_message_at, is_archived, created_at`

func (r *ThreadRepo) Create(ctx context.Context, t *domain.ConversationThread) error {
	const q = `
		INSERT INTO conversation_threads
			(company_id, channel, phone_number, user_id, discord_user_id,
			 lark_chat_id, lark_thread_key, lark_open_id, api_user_ref,
			 title, summary, last_message_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, '')::uuid, NULLIF($5, ''),
			NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''),
			$10, $11, $12)
		RETURNING id, created_at
	`
	return r.db.QueryRowContext(ctx, q,
		t.CompanyID, string(t.Channel), t.PhoneNumber, t.UserID, t.DiscordUserID,
		t.LarkChatID, t.LarkThreadKey, t.LarkOpenID, t.APIUserRef,
		t.Title, t.Summary, t.LastMessageAt,
	).Scan(&t.ID, &t.CreatedAt)
}

func (r *ThreadRepo) GetByID(ctx context.Context, id string) (*domain.ConversationThread, error) {
	q := `SELECT ` + threadSelectCols + ` FROM conversation_threads WHERE id = $1`
	return r.scanOne(ctx, q, id)
}

func (r *ThreadRepo) LatestForPhone(ctx context.Context, companyID, phoneNumber string) (*domain.ConversationThread, error) {
	q := `SELECT ` + threadSelectCols + `
		FROM conversation_threads
		WHERE company_id = $1 AND phone_number = $2 AND NOT is_archived
		ORDER BY last_message_at DESC LIMIT 1`
	return r.scanOne(ctx, q, companyID, phoneNumber)
}

func (r *ThreadRepo) LatestForUser(ctx context.Context, companyID, userID string) (*domain.ConversationThread, error) {
	q := `SELECT ` + threadSelectCols + `
		FROM conversation_threads
		WHERE company_id = $1 AND user_id = $2 AND NOT is_archived
		ORDER BY last_message_at DESC LIMIT 1`
	return r.scanOne(ctx, q, companyID, userID)
}

func (r *ThreadRepo) LatestForDiscordUser(ctx context.Context, companyID, discordUserID string) (*domain.ConversationThread, error) {
	q := `SELECT ` + threadSelectCols + `
		FROM conversation_threads
		WHERE company_id = $1 AND discord_user_id = $2 AND channel = 'discord' AND NOT is_archived
		ORDER BY last_message_at DESC LIMIT 1`
	return r.scanOne(ctx, q, companyID, discordUserID)
}

func (r *ThreadRepo) LatestForLark(ctx context.Context, companyID, larkThreadKey string) (*domain.ConversationThread, error) {
	q := `SELECT ` + threadSelectCols + `
		FROM conversation_threads
		WHERE company_id = $1 AND lark_thread_key = $2 AND channel = 'lark' AND NOT is_archived
		ORDER BY last_message_at DESC LIMIT 1`
	return r.scanOne(ctx, q, companyID, larkThreadKey)
}

// LatestForAPIUser is the `api` channel's lookup (T-A1). The channel filter
// is not redundant with api_user_ref being non-NULL: it is what stops a
// caller's own user reference from ever resolving to a thread that arrived
// on another channel, whatever a future writer puts in that column.
func (r *ThreadRepo) LatestForAPIUser(ctx context.Context, companyID, apiUserRef string) (*domain.ConversationThread, error) {
	q := `SELECT ` + threadSelectCols + `
		FROM conversation_threads
		WHERE company_id = $1 AND api_user_ref = $2 AND channel = 'api' AND NOT is_archived
		ORDER BY last_message_at DESC LIMIT 1`
	return r.scanOne(ctx, q, companyID, apiUserRef)
}

func (r *ThreadRepo) ListByCompany(ctx context.Context, companyID string, limit, offset int) ([]*domain.ConversationThread, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + threadSelectCols + `
		FROM conversation_threads
		WHERE company_id = $1
		ORDER BY last_message_at DESC LIMIT $2 OFFSET $3`
	rows, err := r.db.QueryContext(ctx, q, companyID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ConversationThread
	for rows.Next() {
		t, err := scanThreadRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *ThreadRepo) UpdateSummary(ctx context.Context, id, title, summary string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE conversation_threads SET title = $1, summary = $2 WHERE id = $3`,
		title, summary, id)
	return err
}

func (r *ThreadRepo) Touch(ctx context.Context, id string, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE conversation_threads SET last_message_at = $1 WHERE id = $2`,
		at, id)
	return err
}

func (r *ThreadRepo) Archive(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE conversation_threads SET is_archived = true WHERE id = $1`, id)
	return err
}

func (r *ThreadRepo) Delete(ctx context.Context, id string) error {
	// The DB has ON DELETE CASCADE on messages, so this removes the thread
	// and all of its messages in one go.
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM conversation_threads WHERE id = $1`, id)
	return err
}

func (r *ThreadRepo) scanOne(ctx context.Context, q string, args ...interface{}) (*domain.ConversationThread, error) {
	row := r.db.QueryRowContext(ctx, q, args...)
	t, err := scanThreadRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return t, err
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanThreadRow(row rowScanner) (*domain.ConversationThread, error) {
	t := &domain.ConversationThread{}
	var channel string
	if err := row.Scan(
		&t.ID, &t.CompanyID, &channel, &t.PhoneNumber, &t.UserID, &t.DiscordUserID,
		&t.LarkChatID, &t.LarkThreadKey, &t.LarkOpenID, &t.APIUserRef,
		&t.Title, &t.Summary, &t.LastMessageAt, &t.IsArchived, &t.CreatedAt,
	); err != nil {
		return nil, err
	}
	t.Channel = domain.Channel(channel)
	return t, nil
}
