package domain

import (
	"context"
	"slices"
	"strings"
	"time"
)

// AgentChannelBinding routes one channel address to one roster agent (T-S4).
//
// The dashboard (T-S3) and `/v1` (T-S5) reach the roster by letting the caller
// name an agent. Discord, Lark and WhatsApp have nobody to ask — an inbound
// message carries a user and a room and no place to put a picker — so the
// binding is what an admin configures instead, and it is on the *address*
// rather than on the person: a channel is a room configured for a job, and
// every message that arrives in it is about that job.
//
// Absence is the ordinary state and means the company default. This table holds
// exceptions.
type AgentChannelBinding struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	AgentID   string `json:"agent_id"`
	// AgentName rides along on reads so a bindings table does not have to join
	// the roster in the browser. It is not persisted.
	AgentName string  `json:"agent_name,omitempty"`
	Channel   Channel `json:"channel"`
	// ExternalID is the Discord channel id, the Lark chat id, or the E.164
	// phone number — stored as the provider gives it, except the phone number,
	// which goes through NormalizePhone for the reason stated there.
	ExternalID string    `json:"external_id"`
	CreatedAt  time.Time `json:"created_at"`
	// AgentAccessMode rides along like AgentName: whether the agent this
	// address answers as is open or restricted (T-Z8). It is the agent's, not
	// the binding's, and it is on reads so Settings can show which bindings a
	// restriction has silenced without a second request.
	AgentAccessMode AccessMode `json:"agent_access_mode,omitempty"`
	// RestrictedAcknowledgedAt is when an admin acknowledged, for this address,
	// that anyone who can post there can use its agent while the agent is
	// restricted (roadmap 12, decision 8). Nil means nobody has — and then a
	// restricted agent does not answer here.
	RestrictedAcknowledgedAt *time.Time `json:"restricted_acknowledged_at,omitempty"`
	// RestrictedAcknowledgedBy is the admin who acknowledged it, and empty once
	// that admin's account is deleted; the audit row still names them.
	RestrictedAcknowledgedBy string `json:"restricted_acknowledged_by,omitempty"`
}

// ChannelRoute is what an inbound address resolves to: the agent bound to it
// (T-S4), and whether an admin has cleared the address to reach that agent while
// it is restricted (T-Z8).
//
// Acknowledged is a stored fact, not a decision. Whether the agent is restricted
// is internal/authz's to answer (roadmap 12, decision 1), and the enqueue path
// asks it; this only matters once the answer is "restricted".
type ChannelRoute struct {
	AgentID      string
	Acknowledged bool
}

// BindableChannels are the channels a binding can name.
//
// Not dashboard and not api: both already carry an explicit agent id from the
// caller, and a binding on them would be a second, invisible opinion about a
// turn that already named one.
var BindableChannels = []Channel{ChannelWhatsApp, ChannelDiscord, ChannelLark, ChannelSlack}

// Bindable reports whether a binding may name this channel.
func (c Channel) Bindable() bool { return slices.Contains(BindableChannels, c) }

// NormalizeChannelRef canonicalises an address for storage and for lookup.
//
// One function rather than a rule each caller applies, because the two callers
// are a write in the dashboard and a read on every inbound message: a
// `whatsapp:` prefix or a pasted trailing space on one side and not the other
// is a binding that exists and never fires, with nothing to see in either the
// table or the log.
//
// Discord snowflakes, Lark chat ids and Slack channel ids are opaque provider
// identifiers and are stored exactly as the provider gives them.
func NormalizeChannelRef(c Channel, externalID string) string {
	ref := strings.TrimSpace(externalID)
	if c == ChannelWhatsApp {
		return NormalizePhone(ref)
	}
	return ref
}

// AgentChannelBindingRepository is the persistence contract for the bindings.
//
// Every method takes companyID, for the reason AgentRepository states: a
// repository that can be asked for a binding by id alone is one whose callers
// have to remember the tenant check.
type AgentChannelBindingRepository interface {
	Create(ctx context.Context, b *AgentChannelBinding) error
	ListByCompany(ctx context.Context, companyID string) ([]*AgentChannelBinding, error)
	Delete(ctx context.Context, companyID, id string) error
	// AgentForChannel returns the id of the agent bound to one address, or
	// ErrNotFound when the address is unbound — which the caller reads as "the
	// company default" rather than as a failure.
	//
	// A binding to a *disabled* agent is ErrNotFound too. Disabling is how an
	// admin takes an agent out of service, and leaving a channel pointed at one
	// would make the ops room stop answering with no visible cause.
	AgentForChannel(ctx context.Context, companyID string, channel Channel, externalID string) (ChannelRoute, error)
	// Acknowledge records that actorID acknowledged, for one binding, that
	// anyone who can post on its address can use its agent while it is
	// restricted (T-Z8). An existing acknowledgement is kept as it was, with the
	// admin who made it. ErrNotFound for another company's binding.
	Acknowledge(ctx context.Context, companyID, id, actorID string, at time.Time) (*AgentChannelBinding, error)
}
