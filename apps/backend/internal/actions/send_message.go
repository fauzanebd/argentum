package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
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
	// AttachDocumentID names a document to deliver with the message (T-V3).
	//
	// **It travels as a link, never as bytes, and that is not a shortcut.** The
	// chat channels each cap an upload — Discord at 8–25 MB depending on the
	// tenant's tier, WhatsApp at 16 MB, Lark and Slack at their own numbers —
	// and a video clears the smallest of them routinely: the render gate of
	// 2026-08-09 measured 5.9 MB for 87 seconds of 1080p, so an ordinary
	// three-minute report is past Discord's free limit on its own. The ticket
	// asks for a link *above the threshold*; below it the choice is between a
	// link and an upload path that does not exist, and a link that always works
	// beats one that works until a report gets longer.
	//
	// Silently failing to attach is the worst outcome and silently transcoding
	// to fit is the second worst. A document that cannot be resolved refuses the
	// action rather than sending a message that promises a file it does not
	// carry.
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
	docs DocumentLinker
}

// DocumentLinker resolves a document to a link the recipient can open (T-V3).
//
// Declared at the consumer and narrow, like Messenger: this package has no
// business knowing about object storage, and the presign belongs to whoever
// already owns document URLs. **The company id is a parameter rather than a
// courtesy** — the action executes in a worker on behalf of a tenant, and a
// document id from another tenant must not resolve. The implementation reads
// through the company-scoped lookup for the same reason `/v1` does.
type DocumentLinker interface {
	LinkForDocument(ctx context.Context, companyID, documentID string) (Attachment, error)
}

// Attachment is what a recipient is told about the file.
type Attachment struct {
	Filename  string
	URL       string
	SizeBytes int64
	// ExpiresIn is how long the link lasts, rendered into the message. A
	// recipient who opens it next week and gets a signature error, with nothing
	// in the message that warned them, is a support conversation this sentence
	// prevents.
	ExpiresIn time.Duration
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

// WithDocuments lets this action deliver a document link (T-V3).
//
// Optional, and its absence is not a silent downgrade: without a linker an
// `attach_document_id` is refused at Validate, so a proposal that cannot be
// honoured is never stored and never put in front of a human to approve. That
// is the same rule the format enum follows in `generate_document` — do not
// offer what this process cannot finish.
func (a *SendMessageAction) WithDocuments(docs DocumentLinker) *SendMessageAction {
	a.docs = docs
	return a
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
	p.AttachDocumentID = strings.TrimSpace(p.AttachDocumentID)
	if p.AttachDocumentID != "" && a.docs == nil {
		return p, "", fmt.Errorf("attach_document_id is not deliverable on this deployment; " +
			"send the message without it, or give the recipient the download link in the body")
	}
	return p, ch, nil
}

func (a *SendMessageAction) Validate(params json.RawMessage) error {
	_, _, err := a.parse(params)
	return err
}

// Usage names the channel restriction as well as the parameters, because the
// refusal a model gets for proposing a Discord message is "channel must be
// whatsapp" — a rule it can only learn from a round trip it should not have to
// spend. The allowlist is not stated as a list: recipients are the tenant's, and
// enumerating them into every turn's prompt would put phone numbers in a context
// window to save one refusal.
func (a *SendMessageAction) Usage() string {
	return `send a message on a chat channel to an already-allowlisted recipient. ` +
		`params: {"channel": "whatsapp", "target_ref": "<the allowlisted recipient>", "body": "<the message>"}. ` +
		`Only "whatsapp" is deliverable in this version, and only to a recipient an admin has allowlisted.`
}

func (a *SendMessageAction) Describe(params json.RawMessage) (string, error) {
	p, _, err := a.parse(params)
	if err != nil {
		return "", err
	}
	note := ""
	if p.AttachDocumentID != "" {
		// The card says a document travels and does not say which one, because
		// Describe has no context to resolve it with — it is rendered when the
		// proposal is written and again when it is read, and neither call knows
		// the tenant. The approver sees the id; the message the recipient gets
		// carries the filename.
		note = fmt.Sprintf(" — with a download link to document %s", p.AttachDocumentID)
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
	body := p.Body
	attached := ""
	if p.AttachDocumentID != "" {
		// Resolved before delivery, and a failure refuses the whole action.
		// Sending the message without the file would deliver a sentence about a
		// report with no report in it — the silent shape this codebase keeps
		// finding, and the one an approver cannot check afterwards because the
		// message has already gone.
		att, err := a.docs.LinkForDocument(ctx, tenantctx.CompanyID(ctx), p.AttachDocumentID)
		if err != nil {
			return nil, fmt.Errorf("attach document %s: %w", p.AttachDocumentID, err)
		}
		body += "\n\n" + attachmentLine(att)
		attached = att.Filename
	}
	if err := a.msgr.Send(ctx, ch, p.TargetRef, body); err != nil {
		return nil, fmt.Errorf("deliver message: %w", err)
	}
	result := map[string]string{
		"channel":    p.Channel,
		"target_ref": p.TargetRef,
		"delivered":  "true",
	}
	if attached != "" {
		result["attached"] = attached
	}
	out, _ := json.Marshal(result)
	return out, nil
}

// attachmentLine is what the recipient reads.
//
// A markdown link, because every channel this action can reach renders one and
// the chat runner already flattens markdown links for WhatsApp. The expiry is
// stated rather than left to be discovered: a presigned URL that has lapsed
// answers with a signature error, which reads to a recipient as the product
// being broken rather than as a link having a lifetime.
func attachmentLine(att Attachment) string {
	name := strings.TrimSpace(att.Filename)
	if name == "" {
		name = "the document"
	}
	line := fmt.Sprintf("[%s](%s)", name, att.URL)
	if att.SizeBytes > 0 {
		line += fmt.Sprintf(" · %s", humanSize(att.SizeBytes))
	}
	if att.ExpiresIn > 0 {
		line += fmt.Sprintf(" · link expires in about %s", roughDuration(att.ExpiresIn))
	}
	return line
}

// humanSize is for a person reading a chat message, so it stops at MB and
// rounds hard: "5.9 MB" is the whole of what a recipient wants to know before
// tapping a link on mobile data.
func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

func roughDuration(d time.Duration) string {
	if d >= time.Hour {
		if h := int(d.Round(time.Hour) / time.Hour); h == 1 {
			return "an hour"
		} else {
			return fmt.Sprintf("%d hours", h)
		}
	}
	return fmt.Sprintf("%d minutes", int(d.Round(time.Minute)/time.Minute))
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
