package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

type ThreadRepo struct{ db *sql.DB }

func NewThreadRepo(db *sql.DB) *ThreadRepo { return &ThreadRepo{db: db} }

const threadSelectCols = `id, company_id, channel, COALESCE(phone_number, ''), COALESCE(user_id::text, ''),
		COALESCE(discord_user_id, ''), COALESCE(lark_chat_id, ''), COALESCE(lark_thread_key, ''),
		COALESCE(lark_open_id, ''), COALESCE(api_user_ref, ''),
		COALESCE(embed_user_ref, ''), COALESCE(agent_id::text, ''),
		COALESCE(slack_team_id, ''), COALESCE(slack_channel_id, ''),
		COALESCE(slack_thread_ts, ''), COALESCE(slack_user_id, ''),
		title, summary, last_message_at, is_archived, created_at`

func (r *ThreadRepo) Create(ctx context.Context, t *domain.ConversationThread) error {
	const q = `
		INSERT INTO conversation_threads
			(company_id, channel, phone_number, user_id, discord_user_id,
			 lark_chat_id, lark_thread_key, lark_open_id, api_user_ref, embed_user_ref,
			 agent_id,
			 slack_team_id, slack_channel_id, slack_thread_ts, slack_user_id,
			 title, summary, last_message_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, '')::uuid, NULLIF($5, ''),
			NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''),
			NULLIF($10, ''),
			NULLIF($11, '')::uuid,
			NULLIF($12, ''), NULLIF($13, ''), NULLIF($14, ''), NULLIF($15, ''),
			$16, $17, $18)
		RETURNING id, created_at
	`
	return r.db.QueryRowContext(ctx, q,
		t.CompanyID, string(t.Channel), t.PhoneNumber, t.UserID, t.DiscordUserID,
		t.LarkChatID, t.LarkThreadKey, t.LarkOpenID, t.APIUserRef, t.EmbedUserRef,
		t.AgentID,
		t.SlackTeamID, t.SlackChannelID, t.SlackThreadTS, t.SlackUserID,
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

// LatestForSlackThread finds the conversation a threaded Slack message belongs
// to. Both key columns are matched: Slack's `ts` is unique only within a
// channel, so thread_ts alone would collide across channels.
func (r *ThreadRepo) LatestForSlackThread(ctx context.Context, companyID, slackChannelID, slackThreadTS string) (*domain.ConversationThread, error) {
	q := `SELECT ` + threadSelectCols + `
		FROM conversation_threads
		WHERE company_id = $1 AND slack_channel_id = $2 AND slack_thread_ts = $3
		  AND channel = 'slack' AND NOT is_archived
		ORDER BY last_message_at DESC LIMIT 1`
	return r.scanOne(ctx, q, companyID, slackChannelID, slackThreadTS)
}

// LatestForSlackUser finds the conversation a *top-level* Slack message
// continues — a mention or DM carrying no thread_ts, so there is no thread id
// to look up. Keyed on the room and the person, like Discord.
func (r *ThreadRepo) LatestForSlackUser(ctx context.Context, companyID, slackChannelID, slackUserID string) (*domain.ConversationThread, error) {
	q := `SELECT ` + threadSelectCols + `
		FROM conversation_threads
		WHERE company_id = $1 AND slack_channel_id = $2 AND slack_user_id = $3
		  AND channel = 'slack' AND NOT is_archived
		ORDER BY last_message_at DESC LIMIT 1`
	return r.scanOne(ctx, q, companyID, slackChannelID, slackUserID)
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

// LatestForEmbedUser is the `widget` channel's lookup (T-20). The channel
// filter carries LatestForAPIUser's reasoning and one more: the two ref
// columns hold strings the same tenant chose, so without it a visitor of their
// website and a user of their backend integration could resolve to each
// other's conversations the day anything writes both columns.
func (r *ThreadRepo) LatestForEmbedUser(ctx context.Context, companyID, embedUserRef string) (*domain.ConversationThread, error) {
	q := `SELECT ` + threadSelectCols + `
		FROM conversation_threads
		WHERE company_id = $1 AND embed_user_ref = $2 AND channel = 'widget' AND NOT is_archived
		ORDER BY last_message_at DESC LIMIT 1`
	return r.scanOne(ctx, q, companyID, embedUserRef)
}

// GetForCompany is GetByID with the tenant boundary inside the query (T-A3).
//
// The `/v1` chat surface uses only this one. A malformed uuid fails the cast
// rather than matching nothing, which is why the caller maps every error here
// onto the same not-found: an id from another tenant and an id that is not an
// id must be indistinguishable, or the route is an existence oracle over other
// companies' conversations.
func (r *ThreadRepo) GetForCompany(ctx context.Context, companyID, id string) (*domain.ConversationThread, error) {
	q := `SELECT ` + threadSelectCols + `
		FROM conversation_threads WHERE id = $1 AND company_id = $2`
	return r.scanOne(ctx, q, id, companyID)
}

// maxThreadPage / defaultThreadPage bound a `/v1` page, on the same terms
// documents are bounded: an over-large ask is trimmed rather than refused,
// because the cursor is what makes the rest reachable.
const (
	maxThreadPage     = 100
	defaultThreadPage = 25
)

// ListPage returns one keyset page newest-first plus whether another exists.
//
// Ordered by (created_at, id) rather than by last_message_at, which is what the
// dashboard's listing sorts on: last_message_at *moves*, so a cursor built from
// it names a position that has already changed by the time the next page is
// asked for — a thread that receives a message mid-walk jumps to the front and
// is served twice. created_at never moves. The cost is that a `/v1` listing is
// in creation order rather than in recency order, which is the right trade for
// a machine walking a list once.
func (r *ThreadRepo) ListPage(ctx context.Context, companyID string, f domain.ThreadFilter) ([]*domain.ConversationThread, bool, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = defaultThreadPage
	}
	if limit > maxThreadPage {
		limit = maxThreadPage
	}

	var where strings.Builder
	where.WriteString(` WHERE company_id = $1`)
	args := []any{companyID}
	if f.Channel != "" {
		args = append(args, string(f.Channel))
		where.WriteString(` AND channel = $` + itoa(len(args)))
	}
	if f.APIUserRef != "" {
		args = append(args, f.APIUserRef)
		where.WriteString(` AND api_user_ref = $` + itoa(len(args)))
	}
	if f.EmbedUserRef != "" {
		args = append(args, f.EmbedUserRef)
		where.WriteString(` AND embed_user_ref = $` + itoa(len(args)))
	}
	if f.CursorID != "" && !f.CursorTime.IsZero() {
		args = append(args, f.CursorTime, f.CursorID)
		where.WriteString(` AND (created_at, id) < ($` + itoa(len(args)-1) + `, $` + itoa(len(args)) + `::uuid)`)
	}

	// One row more than asked for, discarded before returning: has_more is then
	// a fact rather than the guess `len(rows) == limit` gives.
	args = append(args, limit+1)
	q := `SELECT ` + threadSelectCols + ` FROM conversation_threads` + where.String() +
		` ORDER BY created_at DESC, id DESC LIMIT $` + itoa(len(args))

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]*domain.ConversationThread, 0, limit)
	for rows.Next() {
		t, err := scanThreadRow(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
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
		&t.LarkChatID, &t.LarkThreadKey, &t.LarkOpenID, &t.APIUserRef, &t.EmbedUserRef,
		&t.AgentID,
		&t.SlackTeamID, &t.SlackChannelID, &t.SlackThreadTS, &t.SlackUserID,
		&t.Title, &t.Summary, &t.LastMessageAt, &t.IsArchived, &t.CreatedAt,
	); err != nil {
		return nil, err
	}
	t.Channel = domain.Channel(channel)
	return t, nil
}
