package slack

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Envelope is the outer shape of every Events API callback. Slack sends
// three top-level types: `url_verification` (the one-shot challenge posted
// when the Request URL is saved), `event_callback` (a real event), and
// `app_rate_limited`. Payloads are never encrypted — authenticity comes
// from the v0 signature instead.
type Envelope struct {
	Token          string          `json:"token,omitempty"`
	Type           string          `json:"type,omitempty"`
	Challenge      string          `json:"challenge,omitempty"`
	TeamID         string          `json:"team_id,omitempty"`
	APIAppID       string          `json:"api_app_id,omitempty"`
	EventID        string          `json:"event_id,omitempty"`
	EventTime      int64           `json:"event_time,omitempty"`
	Event          json.RawMessage `json:"event,omitempty"`
	Authorizations []Authorization `json:"authorizations,omitempty"`
}

// Authorization identifies the installation the event was delivered for.
// Slack sends `is_bot: true` with the bot's own user id, which lets the
// webhook detect self-messages even when bot_user_id was never configured.
type Authorization struct {
	TeamID              string `json:"team_id"`
	UserID              string `json:"user_id"`
	IsBot               bool   `json:"is_bot"`
	IsEnterpriseInstall bool   `json:"is_enterprise_install"`
}

// Event is the inner `event` object. app_mention and message share enough
// shape that one struct covers both. Subtype is set for edits, joins,
// deletions etc. — those are ignored. BotID is set when the message came
// from a bot (including our own replies), which is how echo loops are cut.
type Event struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype,omitempty"`
	User        string `json:"user,omitempty"`
	BotID       string `json:"bot_id,omitempty"`
	Text        string `json:"text,omitempty"`
	TS          string `json:"ts,omitempty"`
	ThreadTS    string `json:"thread_ts,omitempty"`
	Channel     string `json:"channel,omitempty"`
	ChannelType string `json:"channel_type,omitempty"`
	Team        string `json:"team,omitempty"`
}

// Envelope type constants.
const (
	TypeURLVerification = "url_verification"
	TypeEventCallback   = "event_callback"
)

// Inner event type constants.
const (
	EventAppMention = "app_mention"
	EventMessage    = "message"
)

// ErrNotMessageEvent signals to webhook callers that this callback isn't an
// inbound message the agent should answer and should be silently acked.
var ErrNotMessageEvent = errors.New("not a message event")

// ParseEnvelope reads the outer callback body.
func ParseEnvelope(body []byte) (*Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("parse envelope: %w", err)
	}
	return &env, nil
}

// ParseEvent decodes the inner event object of an event_callback.
func ParseEvent(raw json.RawMessage) (*Event, error) {
	if len(raw) == 0 {
		return nil, ErrNotMessageEvent
	}
	var ev Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, fmt.Errorf("parse event: %w", err)
	}
	return &ev, nil
}

// BotUserID returns the bot's own user id from the authorizations array,
// or "" when Slack didn't send one. Used as a fallback when the tenant
// hasn't saved bot_user_id explicitly.
func (e *Envelope) BotUserID() string {
	for _, a := range e.Authorizations {
		if a.IsBot && a.UserID != "" {
			return a.UserID
		}
	}
	return ""
}

// ThreadKey returns the ts that identifies the conversation thread this
// event belongs to: thread_ts when the message is already threaded,
// otherwise the message's own ts (replying to it starts the thread).
func (e *Event) ThreadKey() string {
	if e.ThreadTS != "" {
		return e.ThreadTS
	}
	return e.TS
}

// IsDM reports whether the event came from a direct-message conversation.
// Slack marks those with channel_type "im"; channel ids also start with D,
// which is checked as a fallback since app_mention omits channel_type.
func (e *Event) IsDM() bool {
	return e.ChannelType == "im" || strings.HasPrefix(e.Channel, "D")
}

// Actionable reports whether the agent should answer this event.
//
// Accepted: app_mention anywhere, and plain user messages in a DM. Both
// must be plain messages (no subtype — edits, joins, file shares and
// deletions are ignored) from a human (no bot_id, and not the bot's own
// user id) with non-empty text.
//
// botUserID may be "" when it is unknown; the bot_id check still cuts the
// echo loop because Slack always stamps bot_id on messages a bot posted.
func (e *Event) Actionable(botUserID string) bool {
	if e.Subtype != "" || e.BotID != "" {
		return false
	}
	if e.User == "" || (botUserID != "" && e.User == botUserID) {
		return false
	}
	if strings.TrimSpace(e.Text) == "" || e.Channel == "" || e.TS == "" {
		return false
	}
	switch e.Type {
	case EventAppMention:
		return true
	case EventMessage:
		return e.IsDM()
	default:
		return false
	}
}
