package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// The write half of T-S4. What reaches the table decides what every inbound
// message on that address is answered by, so the refusals matter more than the
// happy path: a binding stored against the wrong company, or against a number
// in the wrong shape, is a routing rule that exists and never fires.

type fakeBindingRepo struct {
	created []*domain.AgentChannelBinding
	list    []*domain.AgentChannelBinding
	err     error
	deleted []string
}

func (f *fakeBindingRepo) Create(_ context.Context, b *domain.AgentChannelBinding) error {
	if f.err != nil {
		return f.err
	}
	b.ID = "bind-1"
	f.created = append(f.created, b)
	return nil
}

func (f *fakeBindingRepo) ListByCompany(context.Context, string) ([]*domain.AgentChannelBinding, error) {
	return f.list, f.err
}

func (f *fakeBindingRepo) Delete(_ context.Context, _, id string) error {
	f.deleted = append(f.deleted, id)
	return f.err
}

func (f *fakeBindingRepo) AgentForChannel(context.Context, string, domain.Channel, string) (string, error) {
	panic("unexpected AgentForChannel: the CRUD service never resolves a turn")
}

// fakeAgentRepo answers GetByID from the same "companyID/agentID" key the
// enqueuer's stub uses, so an id belonging to another tenant misses.
type fakeAgentRepo struct{ byID map[string]*domain.Agent }

func (f *fakeAgentRepo) Create(context.Context, *domain.Agent) error { panic("unexpected Create") }
func (f *fakeAgentRepo) GetByID(_ context.Context, companyID, id string) (*domain.Agent, error) {
	if a, ok := f.byID[companyID+"/"+id]; ok {
		return a, nil
	}
	return nil, domain.ErrNotFound
}
func (f *fakeAgentRepo) GetDefault(context.Context, string) (*domain.Agent, error) {
	panic("unexpected GetDefault")
}
func (f *fakeAgentRepo) ListByCompany(context.Context, string) ([]*domain.Agent, error) {
	panic("unexpected ListByCompany")
}
func (f *fakeAgentRepo) Update(context.Context, *domain.Agent) error { panic("unexpected Update") }
func (f *fakeAgentRepo) Delete(context.Context, string, string) error {
	panic("unexpected Delete")
}
func (f *fakeAgentRepo) SetDefault(context.Context, string, string) error {
	panic("unexpected SetDefault")
}

func bindingService(repo *fakeBindingRepo, agents ...*domain.Agent) *AgentBindingService {
	byID := map[string]*domain.Agent{}
	for _, a := range agents {
		byID[a.CompanyID+"/"+a.ID] = a
	}
	return NewAgentBindingService(repo, &fakeAgentRepo{byID: byID})
}

func TestABindingIsStoredAgainstTheAddress(t *testing.T) {
	repo := &fakeBindingRepo{}
	svc := bindingService(repo, &domain.Agent{ID: "ag-ops", CompanyID: "co-1", Enabled: true})

	b, err := svc.Create(context.Background(), "co-1", BindingInput{
		AgentID: "ag-ops", Channel: "discord", ExternalID: "chan-ops",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.AgentID != "ag-ops" || b.Channel != domain.ChannelDiscord || b.ExternalID != "chan-ops" {
		t.Errorf("stored %+v", b)
	}
}

// The write end of the same normalisation the lookup does. An admin who pastes
// what WhatsApp showed them must not get a binding that never matches.
func TestAWhatsAppBindingIsStoredNormalised(t *testing.T) {
	repo := &fakeBindingRepo{}
	svc := bindingService(repo, &domain.Agent{ID: "ag-fin", CompanyID: "co-1", Enabled: true})

	b, err := svc.Create(context.Background(), "co-1", BindingInput{
		AgentID: "ag-fin", Channel: "whatsapp", ExternalID: " whatsapp:+628123 ",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.ExternalID != "+628123" {
		t.Errorf("stored %q, want the normalised number", b.ExternalID)
	}
}

// Another company's agent and one that never existed answer the same way, and
// neither confirms the row exists: the same treatment AgentService gives a
// source id it does not own.
func TestABindingCannotNameAnotherCompanysAgent(t *testing.T) {
	repo := &fakeBindingRepo{}
	svc := bindingService(repo,
		&domain.Agent{ID: "ag-ops", CompanyID: "co-1", Enabled: true},
		&domain.Agent{ID: "ag-other", CompanyID: "co-2", Enabled: true},
	)

	for name, id := range map[string]string{
		"another company's agent":  "ag-other",
		"an id that never existed": "ag-nope",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Create(context.Background(), "co-1", BindingInput{
				AgentID: id, Channel: "discord", ExternalID: "chan-ops",
			})
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("Create error = %v, want ErrInvalidInput", err)
			}
			if msg := err.Error(); !strings.Contains(msg, "no such agent") {
				t.Errorf("error text = %q; both refusals must read the same", msg)
			}
			if len(repo.created) != 0 {
				t.Error("a binding was written for an agent the company does not own")
			}
		})
	}
}

// The dashboard and the API are already told which agent to use. A binding on
// them would be a second, invisible opinion about a turn that named one.
func TestOnlyTheInboundChatChannelsCanBeBound(t *testing.T) {
	svc := bindingService(&fakeBindingRepo{}, &domain.Agent{ID: "ag-ops", CompanyID: "co-1", Enabled: true})

	for _, channel := range []string{"dashboard", "api", "", "slack"} {
		t.Run(channel, func(t *testing.T) {
			_, err := svc.Create(context.Background(), "co-1", BindingInput{
				AgentID: "ag-ops", Channel: channel, ExternalID: "whatever",
			})
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Errorf("Create(%q) error = %v, want ErrInvalidInput", channel, err)
			}
		})
	}
	for _, channel := range domain.BindableChannels {
		if !channel.Bindable() {
			t.Errorf("%q is in BindableChannels and reports itself unbindable", channel)
		}
	}
}

// Re-pointing a live channel is a decision. Overwriting the existing binding
// would make it one nobody made, so the unique index refuses and the API says
// 409 — which is also the acceptance item this ticket carries.
func TestASecondBindingOnOneAddressIsRefused(t *testing.T) {
	repo := &fakeBindingRepo{err: domain.ErrAlreadyExists}
	svc := bindingService(repo, &domain.Agent{ID: "ag-ops", CompanyID: "co-1", Enabled: true})

	_, err := svc.Create(context.Background(), "co-1", BindingInput{
		AgentID: "ag-ops", Channel: "discord", ExternalID: "chan-ops",
	})
	if !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("Create error = %v, want ErrAlreadyExists", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "chan-ops") {
		t.Errorf("error text = %q; it has to name the address that is already bound", msg)
	}
}

func TestAnEmptyOrOversizedIdentifierIsRefused(t *testing.T) {
	svc := bindingService(&fakeBindingRepo{}, &domain.Agent{ID: "ag-ops", CompanyID: "co-1", Enabled: true})

	cases := map[string]string{
		"empty":      "   ",
		"a paste":    strings.Repeat("9", bindingRefMax+1),
		"whatsapp:":  "whatsapp:",
		"whitespace": "\t\n",
	}
	for name, ref := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Create(context.Background(), "co-1", BindingInput{
				AgentID: "ag-ops", Channel: "whatsapp", ExternalID: ref,
			})
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Errorf("Create(%q) error = %v, want ErrInvalidInput", ref, err)
			}
		})
	}
}

// The channel names the identifier it wants. "external_id required" sends an
// admin to the API docs; "a Discord channel id is required" sends them to
// Discord.
func TestTheRefusalNamesWhatTheChannelWants(t *testing.T) {
	svc := bindingService(&fakeBindingRepo{}, &domain.Agent{ID: "ag-ops", CompanyID: "co-1", Enabled: true})

	for channel, want := range map[string]string{
		"discord":  "Discord channel id",
		"lark":     "Lark chat id",
		"whatsapp": "phone number",
	} {
		_, err := svc.Create(context.Background(), "co-1", BindingInput{
			AgentID: "ag-ops", Channel: channel, ExternalID: "",
		})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Create(%q) error = %v, want it to name %q", channel, err, want)
		}
	}
}

func TestListNeverReturnsANilSlice(t *testing.T) {
	// A nil slice marshals to `null`, and the bindings table would render
	// nothing rather than its empty state.
	got, err := bindingService(&fakeBindingRepo{}).List(context.Background(), "co-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got == nil {
		t.Error("List returned nil; it has to be an empty slice")
	}
}
