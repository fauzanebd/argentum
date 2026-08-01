package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
)

// Messenger is the narrow delivery contract send_message depends on (T-12a).
// Declared here, where it is consumed, and implemented in internal/app where the
// outbound providers and the allowlist repositories both live. Two methods, and
// the order they are called in is the whole safety property: Allowed gates who a
// message may reach, and it is checked before Send is ever called.
//
// The company is resolved from ctx (tenantctx), exactly as every other tenant
// operation resolves it — the action does not restate what the turn already set.
type Messenger interface {
	// Allowed reports whether targetRef is an allowlisted recipient on channel for
	// the company in ctx. An action must never be able to message an arbitrary
	// number, so a target that is not already on the company's allowlist is not a
	// target this action can reach.
	Allowed(ctx context.Context, channel domain.Channel, targetRef string) (bool, error)
	// Send delivers body to targetRef on channel, reusing the same outbound
	// provider the chat runner uses for that channel.
	Send(ctx context.Context, channel domain.Channel, targetRef, body string) error
}

// sendMessageParams is the shape the agent proposes and a human approves.
type sendMessageParams struct {
	Channel   string `json:"channel"`
	TargetRef string `json:"target_ref"`
	Body      string `json:"body"`
	// AttachDocumentID is accepted for forward compatibility with a report
	// delivery flow (the backlog's "scheduled branded report delivery") but is not
	// yet delivered — validated as a well-formed id if present, never fetched.
	// Ignored rather than rejected so an agent that fills it does not fail the
	// proposal; the sentence Describe renders says plainly it is not attached.
	AttachDocumentID string `json:"attach_document_id,omitempty"`
}

// SendMessageAction is the action that makes watchers useful (T-12a): the agent
// can brief people on a channel, not only answer the person who asked. It sends
// only to already-allowlisted recipients — the allowlist is the whole guardrail,
// enforced in Execute before a single byte is delivered.
//
// Channel scope is WhatsApp in this version. Discord and Lark allowlist inbound
// *users*, while delivery on those channels addresses a *channel* or a *chat* —
// a different identifier space, so "send only to an allowlisted ref" has no safe
// meaning there yet without a channel-level allowlist that does not exist. Adding
// them is additive against Messenger; the gap is recorded in
// coverage/action-framework.md rather than closed with an unsafe guess.
type SendMessageAction struct {
	msgr Messenger
}

// NewSendMessageAction wires the action to a delivery adapter. A nil messenger is
// a wiring error the constructor does not hide: an action that cannot send is not
// an action, so it panics at boot rather than accepting every proposal and
// failing every execution.
func NewSendMessageAction(msgr Messenger) *SendMessageAction {
	if msgr == nil {
		panic("actions: send_message requires a Messenger")
	}
	return &SendMessageAction{msgr: msgr}
}

func (a *SendMessageAction) Kind() string { return "send_message" }

// sendMessageChannels are the channels this action can address safely. WhatsApp
// only, for the reason stated on the type. A channel outside this set is refused
// at Validate, before a proposal is stored.
var sendMessageChannels = map[string]domain.Channel{
	string(domain.ChannelWhatsApp): domain.ChannelWhatsApp,
}

func (a *SendMessageAction) parse(params json.RawMessage) (sendMessageParams, domain.Channel, error) {
	var p sendMessageParams
	if err := json.Unmarshal(params, &p); err != nil {
		return p, "", fmt.Errorf("invalid parameters: %w", err)
	}
	p.Channel = strings.TrimSpace(strings.ToLower(p.Channel))
	p.TargetRef = strings.TrimSpace(p.TargetRef)
	p.Body = strings.TrimSpace(p.Body)
	ch, ok := sendMessageChannels[p.Channel]
	if !ok {
		return p, "", fmt.Errorf("channel %q is not supported for send_message; use one of %v",
			p.Channel, sendMessageChannelNames())
	}
	if p.TargetRef == "" {
		return p, "", fmt.Errorf("target_ref is required — the recipient to message")
	}
	if p.Body == "" {
		return p, "", fmt.Errorf("body is required — the message to send")
	}
	return p, ch, nil
}

func (a *SendMessageAction) Validate(params json.RawMessage) error {
	_, _, err := a.parse(params)
	return err
}

func (a *SendMessageAction) Describe(params json.RawMessage) (string, error) {
	p, _, err := a.parse(params)
	if err != nil {
		return "", err
	}
	note := ""
	if p.AttachDocumentID != "" {
		note = " (a document was requested but is not attached in this version)"
	}
	return fmt.Sprintf("Send a %s message to %s: %q%s", p.Channel, p.TargetRef, preview(p.Body), note), nil
}

func (a *SendMessageAction) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	p, ch, err := a.parse(params)
	if err != nil {
		return nil, err
	}
	// The allowlist check is the guardrail, and it runs before delivery. A target
	// the company has not already authorized is refused here — an approved
	// proposal to an un-allowlisted number still does not send.
	allowed, err := a.msgr.Allowed(ctx, ch, p.TargetRef)
	if err != nil {
		return nil, fmt.Errorf("check recipient allowlist: %w", err)
	}
	if !allowed {
		return nil, fmt.Errorf("%w: %s is not on this workspace's %s allowlist; an admin must add it first",
			domain.ErrInvalidInput, p.TargetRef, p.Channel)
	}
	if err := a.msgr.Send(ctx, ch, p.TargetRef, p.Body); err != nil {
		return nil, fmt.Errorf("deliver message: %w", err)
	}
	out, _ := json.Marshal(map[string]string{
		"channel":    p.Channel,
		"target_ref": p.TargetRef,
		"delivered":  "true",
	})
	return out, nil
}

func sendMessageChannelNames() []string {
	out := make([]string, 0, len(sendMessageChannels))
	for name := range sendMessageChannels {
		out = append(out, name)
	}
	return out
}

// preview trims a body to a short, single-line form for the approval sentence, so
// a long broadcast does not fill the card with the whole message.
func preview(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 80
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
