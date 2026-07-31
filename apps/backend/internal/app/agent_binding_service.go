package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"

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

// AgentBindingService is Settings → Agents' bindings table.
type AgentBindingService struct {
	bindings domain.AgentChannelBindingRepository
	agents   domain.AgentRepository
}

// NewAgentBindingService wires the bindings. agents is the roster the submitted
// agent id is checked against — the repository re-checks it inside the INSERT,
// and doing it here is what turns "no such agent" into a sentence rather than a
// row that quietly did not appear.
func NewAgentBindingService(
	bindings domain.AgentChannelBindingRepository, agents domain.AgentRepository,
) *AgentBindingService {
	return &AgentBindingService{bindings: bindings, agents: agents}
}

// BindingInput is one submitted binding.
type BindingInput struct {
	AgentID    string `json:"agent_id"`
	Channel    string `json:"channel"`
	ExternalID string `json:"external_id"`
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

// Create binds one address to one agent.
//
// A duplicate is ErrAlreadyExists from the unique index rather than an update
// in place: re-pointing a live channel at a different agent is a decision, and
// silently overwriting the old binding would make it one nobody made.
func (s *AgentBindingService) Create(
	ctx context.Context, companyID string, in BindingInput,
) (*domain.AgentChannelBinding, error) {
	channel := domain.Channel(in.Channel)
	ref := domain.NormalizeChannelRef(channel, in.ExternalID)
	switch {
	case companyID == "":
		return nil, fmt.Errorf("%w: a company is required", domain.ErrInvalidInput)
	case !channel.Bindable():
		return nil, fmt.Errorf("%w: %q cannot be bound; choose one of whatsapp, discord or lark",
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
	if _, err := s.agents.GetByID(ctx, companyID, in.AgentID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("%w: no such agent", domain.ErrInvalidInput)
		}
		return nil, fmt.Errorf("lookup agent: %w", err)
	}

	b := &domain.AgentChannelBinding{
		CompanyID:  companyID,
		AgentID:    in.AgentID,
		Channel:    channel,
		ExternalID: ref,
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
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "agent_id": b.AgentID,
		"channel": b.Channel, "external_id": b.ExternalID,
	}).Info("channel binding created")
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
	default:
		return "an identifier is required"
	}
}
