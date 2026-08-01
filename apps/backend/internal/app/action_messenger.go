package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/whatsapp"
)

// ActionMessenger delivers send_message's messages (T-12a) by reusing the same
// outbound providers the chat runner uses, and gates every send on the same
// allowlist the inbound path checks. It satisfies actions.Messenger.
//
// It lives here, not in internal/actions, because it depends on the WhatsApp
// provider and the phone allowlist repository — infrastructure internal/actions
// must not import if the Action contract is to stay free of delivery concerns.
type ActionMessenger struct {
	phones domain.PhoneRepository
	wa     whatsapp.Provider
}

// NewActionMessenger wires the adapter. Either dependency may be nil on a
// deployment that has not configured WhatsApp; a send to a channel whose provider
// is absent is refused at Send with a clear error rather than a nil panic.
func NewActionMessenger(phones domain.PhoneRepository, wa whatsapp.Provider) *ActionMessenger {
	return &ActionMessenger{phones: phones, wa: wa}
}

// SetWhatsApp installs the WhatsApp provider after construction. The worker builds
// its action registry in Configure, before the provider exists — the provider
// arrives with NewChatRunner, the delivery entry point — so the messenger is
// created provider-less and completed here by reference. A no-op on nil, so the
// eval harness (which never delivers) leaves it unset.
func (m *ActionMessenger) SetWhatsApp(wa whatsapp.Provider) {
	if wa != nil {
		m.wa = wa
	}
}

// Allowed reports whether targetRef is an allowlisted recipient for the company
// on ctx. For WhatsApp the allowlist is the phone list, and a number is allowed
// only when it resolves to *this* company — FindCompanyByPhone is globally
// unique, so a number on another tenant's list must not read as allowed here.
func (m *ActionMessenger) Allowed(ctx context.Context, channel domain.Channel, targetRef string) (bool, error) {
	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return false, fmt.Errorf("no tenant in context")
	}
	switch channel {
	case domain.ChannelWhatsApp:
		if m.phones == nil {
			return false, nil
		}
		owner, err := m.phones.FindCompanyByPhone(ctx, domain.NormalizePhone(targetRef))
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return owner.CompanyID == companyID, nil
	default:
		return false, fmt.Errorf("channel %q is not supported for send_message", channel)
	}
}

// Send delivers body to targetRef on channel. It does not re-check the allowlist:
// SendMessageAction.Execute calls Allowed first and refuses on a false, and a
// second lookup here would only invite the two to drift.
func (m *ActionMessenger) Send(ctx context.Context, channel domain.Channel, targetRef, body string) error {
	switch channel {
	case domain.ChannelWhatsApp:
		if m.wa == nil {
			return fmt.Errorf("whatsapp is not configured on this deployment")
		}
		return m.wa.SendMessage(domain.NormalizePhone(targetRef), body)
	default:
		return fmt.Errorf("channel %q is not supported for send_message", channel)
	}
}
