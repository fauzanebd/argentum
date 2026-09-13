package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// Channel bindings (T-S4).
//
// The roster reaches the dashboard by picker and `/v1` by request field. The
// chat channels have neither: an inbound Discord message carries a user and a
// room, and until this service every one of them ran as the company default.
// A binding is the admin's answer to "who should answer in here", stored
// against the address rather than against the person.
//
// This is the CRUD half. The read that matters — which agent answers this
// message — is ChatEnqueuer's, through the same repository.

// bindingRefMax bounds a pasted identifier. A Discord snowflake is 19 digits,
// a Lark chat id ~30 characters, an E.164 number 15; anything past this is a
// paste of the wrong thing entirely, and saying so beats storing it.
const bindingRefMax = 128

// RestrictedBindingAcknowledgement is roadmap 12's decision 8 in its own words:
// what an admin agrees to when they let a restricted agent answer on a channel
// (T-Z8). It is written onto the audit row, so what was agreed to is on the
// record beside who agreed to it.
const RestrictedBindingAcknowledgement = "Anyone who can post here can use this agent."

// ErrBindingNeedsAcknowledgement is a binding to a restricted agent submitted
// without the acknowledgement (T-Z8).
//
// Refused rather than bound silent. Decision 8 is that the channel is the grant
// — who may read `#hr` is Discord's ACL, which this product does not have and
// must not pretend to — so a private channel for a restricted agent is a real
// boundary and binding one is allowed. What is not allowed is doing it without
// saying so out loud, because an admin who restricted HR on Settings → Team
// would otherwise reasonably believe every door to it closed.
var ErrBindingNeedsAcknowledgement = errors.New("binding a restricted agent needs an acknowledgement")

// AgentBindingService is Settings → Agents' bindings table.
type AgentBindingService struct {
	bindings domain.AgentChannelBindingRepository
	agents   domain.AgentRepository
	// access and audit are T-Z8's: whether the agent being bound is restricted,
	// and where an admin's acknowledgement that it may answer here is recorded.
	// Nil access binds exactly as before roadmap 12, and asks nobody anything.
	access AgentAccess
	audit  BindingAudit
	now    func() time.Time
}

// BindingAudit is where an acknowledgement is recorded: T-05's audit log.
// domain.AgentActionRepository satisfies it; declared narrow so this service
// cannot read the log it writes to.
type BindingAudit interface {
	Create(ctx context.Context, a *domain.AgentAction) error
}

// NewAgentBindingService wires the bindings. agents is the roster the submitted
// agent id is checked against — the repository re-checks it inside the INSERT,
// and doing it here is what turns "no such agent" into a sentence rather than a
// row that quietly did not appear.
func NewAgentBindingService(
	bindings domain.AgentChannelBindingRepository, agents domain.AgentRepository,
) *AgentBindingService {
	return &AgentBindingService{bindings: bindings, agents: agents, now: time.Now}
}

// WithAccess makes binding a restricted agent ask for an acknowledgement and
// record it (T-Z8). access is the same internal/authz every other seam decides
// with; audit is the log the acknowledgement is written to.
func (s *AgentBindingService) WithAccess(access AgentAccess, audit BindingAudit) *AgentBindingService {
	s.access = access
	s.audit = audit
	return s
}

// BindingInput is one submitted binding.
type BindingInput struct {
	AgentID    string `json:"agent_id"`
	Channel    string `json:"channel"`
	ExternalID string `json:"external_id"`
	// AcknowledgeRestricted is the admin ticking RestrictedBindingAcknowledgement
	// on the form (T-Z8). Required when the agent is restricted, and ignored when
	// it is open: an acknowledgement recorded ahead of a restriction would be one
	// nobody made about it.
	AcknowledgeRestricted bool `json:"acknowledge_restricted"`
}

// List returns the company's bindings, each carrying its agent's name.
func (s *AgentBindingService) List(ctx context.Context, companyID string) ([]*domain.AgentChannelBinding, error) {
	out, err := s.bindings.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []*domain.AgentChannelBinding{}
	}
	return out, nil
}

// Create binds one address to one agent. actorID is the admin binding it, who is
// named on the audit row when the agent is restricted.
//
// A duplicate is ErrAlreadyExists from the unique index rather than an update
// in place: re-pointing a live channel at a different agent is a decision, and
// silently overwriting the old binding would make it one nobody made.
func (s *AgentBindingService) Create(
	ctx context.Context, companyID, actorID string, in BindingInput,
) (*domain.AgentChannelBinding, error) {
	channel := domain.Channel(in.Channel)
	ref := domain.NormalizeChannelRef(channel, in.ExternalID)
	switch {
	case companyID == "":
		return nil, fmt.Errorf("%w: a company is required", domain.ErrInvalidInput)
	case !channel.Bindable():
		return nil, fmt.Errorf("%w: %q cannot be bound; choose one of whatsapp, discord, lark or slack",
			domain.ErrInvalidInput, in.Channel)
	case in.AgentID == "":
		return nil, fmt.Errorf("%w: choose an agent to bind", domain.ErrInvalidInput)
	case ref == "":
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidInput, bindingRefPrompt(channel))
	case len(ref) > bindingRefMax:
		return nil, fmt.Errorf("%w: that identifier is longer than %d characters — check what was pasted",
			domain.ErrInvalidInput, bindingRefMax)
	}

	// Another company's agent and one that never existed answer the same way,
	// and neither is a 404 on this route: the caller is creating a binding, and
	// the thing that does not exist is a field in their request body. Same
	// answer AgentService.normalizeSources gives for a source id it does not
	// own.
	agent, err := s.agents.GetByID(ctx, companyID, in.AgentID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("%w: no such agent", domain.ErrInvalidInput)
		}
		return nil, fmt.Errorf("lookup agent: %w", err)
	}

	restricted, err := s.restricted(ctx, companyID, agent.ID)
	if err != nil {
		return nil, err
	}
	if restricted && !in.AcknowledgeRestricted {
		return nil, fmt.Errorf("%w: %w: %s is restricted, and binding it means anyone who can post here can use it",
			domain.ErrInvalidInput, ErrBindingNeedsAcknowledgement, agent.Name)
	}
	if restricted && s.audit == nil {
		// No acknowledgement without its record, so none is accepted when
		// there is nowhere to record it.
		return nil, errors.New("the audit log is not configured, so a restricted agent cannot be bound")
	}

	b := &domain.AgentChannelBinding{
		CompanyID:  companyID,
		AgentID:    agent.ID,
		AgentName:  agent.Name,
		Channel:    channel,
		ExternalID: ref,
	}
	if s.access != nil {
		b.AgentAccessMode = domain.AccessModeOpen
	}
	if restricted {
		at := s.now().UTC()
		b.AgentAccessMode = domain.AccessModeRestricted
		b.RestrictedAcknowledgedAt = &at
		b.RestrictedAcknowledgedBy = actorID
	}
	if err := s.bindings.Create(ctx, b); err != nil {
		switch {
		case errors.Is(err, domain.ErrAlreadyExists):
			return nil, fmt.Errorf("%w: %s is already bound to an agent — remove that binding first",
				domain.ErrAlreadyExists, ref)
		case errors.Is(err, domain.ErrNotFound):
			// The repository's own company check. Reachable only in a race with
			// a deletion between the lookup above and this insert.
			return nil, fmt.Errorf("%w: no such agent", domain.ErrInvalidInput)
		}
		return nil, err
	}
	if restricted {
		// Written after the binding, because its id is part of the record, and
		// undone with it: a binding that answers for a restricted agent with no
		// row saying who allowed it is the thing decision 12 exists to prevent.
		// The window in which it could answer is the length of one failed insert.
		if err := s.recordAcknowledgement(ctx, b, actorID); err != nil {
			if derr := s.bindings.Delete(context.WithoutCancel(ctx), companyID, b.ID); derr != nil {
				logrus.WithError(derr).WithFields(logrus.Fields{"company_id": companyID, "binding_id": b.ID}).
					Error("a restricted agent's binding could not be audited or removed; remove it by hand")
			}
			return nil, fmt.Errorf("record the acknowledgement, so nothing was bound: %w", err)
		}
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "agent_id": b.AgentID,
		"channel": b.Channel, "external_id": b.ExternalID, "restricted": restricted,
	}).Info("channel binding created")
	return b, nil
}

// Acknowledge clears an existing binding's address to reach its agent while the
// agent is restricted (T-Z8). It is how a binding made before a restriction
// answers again: restricting an agent silences every binding to it that nobody
// acknowledged, because nobody decided that those rooms may use it.
//
// Refused on an open agent, for BindingInput.AcknowledgeRestricted's reason, and
// idempotent on one already acknowledged — the first admin stays on the record
// and no second row is written.
//
// The audit row is written **before** the acknowledgement, the opposite order to
// Create's, because an acknowledgement has no delete to undo it with. If the
// write after the row fails, the record names an acknowledgement that did not
// take, the channel stays silent, and the admin is told to try again — which is
// the failure that closes rather than the one that opens.
func (s *AgentBindingService) Acknowledge(
	ctx context.Context, companyID, actorID, id string,
) (*domain.AgentChannelBinding, error) {
	current, err := s.find(ctx, companyID, id)
	if err != nil {
		return nil, err
	}
	restricted, err := s.restricted(ctx, companyID, current.AgentID)
	if err != nil {
		return nil, err
	}
	if !restricted {
		return nil, fmt.Errorf("%w: %s is open to everyone, so there is nothing to acknowledge",
			domain.ErrConflict, current.AgentName)
	}
	if current.RestrictedAcknowledgedAt != nil {
		return current, nil
	}
	if err := s.recordAcknowledgement(ctx, current, actorID); err != nil {
		return nil, fmt.Errorf("record the acknowledgement: %w", err)
	}
	b, err := s.bindings.Acknowledge(ctx, companyID, current.ID, actorID, s.now().UTC())
	if err != nil {
		return nil, err
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "binding_id": b.ID, "agent_id": b.AgentID, "channel": b.Channel,
	}).Info("channel binding acknowledged for a restricted agent")
	return b, nil
}

// Delete removes a binding. The address falls back to the company default on
// its next message, which is where it was before the binding existed.
func (s *AgentBindingService) Delete(ctx context.Context, companyID, id string) error {
	if err := s.bindings.Delete(ctx, companyID, id); err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{"company_id": companyID, "binding_id": id}).
		Info("channel binding removed")
	return nil
}

// restricted asks internal/authz whether an agent is restricted, as nobody —
// who holds no grant, so a refusal means restricted and nothing else. Not found
// is a race with a deletion, which the repository's own company check answers.
func (s *AgentBindingService) restricted(ctx context.Context, companyID, agentID string) (bool, error) {
	if s.access == nil {
		return false, nil
	}
	d, err := s.access.Decide(ctx, authz.Subject{CompanyID: companyID}, domain.ResourceKindAgent, agentID)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrAccessCheckFailed, err)
	}
	return d.Reason == authz.ReasonNotGranted, nil
}

// find is one binding of the company's. The list is read rather than a row by
// id because a company's bindings are a handful of rooms, and a repository
// method that answers one binding by id is one more place the tenant check has
// to be remembered.
func (s *AgentBindingService) find(ctx context.Context, companyID, id string) (*domain.AgentChannelBinding, error) {
	all, err := s.bindings.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	for _, b := range all {
		if b != nil && strings.EqualFold(b.ID, strings.TrimSpace(id)) {
			return b, nil
		}
	}
	return nil, domain.ErrNotFound
}

// recordAcknowledgement writes the audit row that names the admin (T-Z8's
// acceptance, decision 12). The actor is the admin, not an agent — nothing ran —
// so the row carries the pseudo tool name the action framework's decisions use
// (`action:approve`), and the channel and address it cleared.
func (s *AgentBindingService) recordAcknowledgement(ctx context.Context, b *domain.AgentChannelBinding, actorID string) error {
	if s.audit == nil {
		return errors.New("the audit log is not configured")
	}
	args, err := json.Marshal(map[string]any{
		"binding_id":      b.ID,
		"agent_id":        b.AgentID,
		"agent_name":      b.AgentName,
		"channel":         b.Channel,
		"external_id":     b.ExternalID,
		"acknowledgement": RestrictedBindingAcknowledgement,
	})
	if err != nil {
		return err
	}
	row := &domain.AgentAction{
		CompanyID:    b.CompanyID,
		ActorKind:    domain.ActorKindUser,
		ActorRef:     actorID,
		Channel:      b.Channel,
		AgentID:      b.AgentID,
		ToolName:     "agent_binding.acknowledge_restricted",
		ArgsRedacted: args,
		ArgsHash:     sha256Hex(string(args)),
		ResultStatus: domain.ActionStatusOK,
	}
	// Detached from the request, with its own deadline, for auditDecision's
	// reason: a client that hangs up after pressing the button must not be the
	// reason the record of what they pressed is lost.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return s.audit.Create(writeCtx, row)
}

// bindingRefPrompt names the identifier the channel actually wants. "external_id
// required" sends an admin to the API docs; "a Discord channel id" sends them
// to Discord.
func bindingRefPrompt(c domain.Channel) string {
	switch c {
	case domain.ChannelDiscord:
		return "a Discord channel id is required"
	case domain.ChannelLark:
		return "a Lark chat id is required"
	case domain.ChannelWhatsApp:
		return "a phone number is required"
	case domain.ChannelSlack:
		return "a Slack channel id is required"
	default:
		return "an identifier is required"
	}
}
