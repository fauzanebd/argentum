package app

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/fauzanebd/argentum/internal/domain"
)

// RIZ-97: the workspace chat model picker. The service's rules — what may be
// chosen, what choosing the default does, and which rows are not the admin's
// to touch — against a repository that remembers what it was told, because
// every acceptance line here is about the row left behind.

const (
	kimi     = "moonshotai/kimi-k2.6"
	deepseek = "deepseek/deepseek-v4.1-flash"
)

// memLLMCreds is CompanyLLMCredentialRepository over a map keyed by
// (company, tier), with the Postgres repo's Upsert semantics: the whole row is
// replaced and updated_at moves.
type memLLMCreds struct {
	rows    map[string]*domain.CompanyLLMCredential
	getErr  error
	upErr   error
	delErr  error
	upserts int
	deletes int
	// foreign, when set, is returned alongside the caller's rows — a repo bug
	// the service must refuse rather than act on.
	foreign *domain.CompanyLLMCredential
}

func newMemLLMCreds(rows ...*domain.CompanyLLMCredential) *memLLMCreds {
	m := &memLLMCreds{rows: map[string]*domain.CompanyLLMCredential{}}
	for _, r := range rows {
		m.rows[r.CompanyID+"|"+string(r.Tier)] = r
	}
	return m
}

func (m *memLLMCreds) GetByCompany(_ context.Context, companyID string) ([]*domain.CompanyLLMCredential, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	var out []*domain.CompanyLLMCredential
	if m.foreign != nil {
		out = append(out, m.foreign)
	}
	for _, r := range m.rows {
		if r.CompanyID == companyID {
			c := *r
			out = append(out, &c)
		}
	}
	return out, nil
}

func (m *memLLMCreds) Upsert(_ context.Context, c *domain.CompanyLLMCredential) error {
	if m.upErr != nil {
		return m.upErr
	}
	m.upserts++
	c.UpdatedAt = time.Now()
	stored := *c
	m.rows[c.CompanyID+"|"+string(c.Tier)] = &stored
	return nil
}

func (m *memLLMCreds) Delete(_ context.Context, companyID string, tier domain.LLMTier) error {
	if m.delErr != nil {
		return m.delErr
	}
	m.deletes++
	delete(m.rows, companyID+"|"+string(tier))
	return nil
}

func (m *memLLMCreds) primary(companyID string) *domain.CompanyLLMCredential {
	return m.rows[companyID+"|"+string(domain.LLMTierPrimary)]
}

func primaryRow(companyID string, edit func(*domain.CompanyLLMCredential)) *domain.CompanyLLMCredential {
	r := &domain.CompanyLLMCredential{ID: "row-1", CompanyID: companyID, Tier: domain.LLMTierPrimary}
	if edit != nil {
		edit(r)
	}
	return r
}

// R5. The allow-list's reason to exist is that nothing on it bills at the
// fallback rate; a model added without a pricing row fails here, not on a
// tenant's invoice.
func TestEveryChatModelIsPriced(t *testing.T) {
	for _, o := range ChatModels {
		if _, ok := LookupModelPricing(o.Model); !ok {
			t.Errorf("chat model %q has no row in modelPricing; it would bill at DefaultPricing", o.Model)
		}
		if o.Label == "" || o.Hint == "" {
			t.Errorf("chat model %q needs a label and a hint for the picker", o.Model)
		}
	}
}

func TestChatModelOptions(t *testing.T) {
	t.Run("production: Kimi is the default and the list is exactly two", func(t *testing.T) {
		got := NewChatModelService(newMemLLMCreds(), kimi).options
		if len(got) != 2 {
			t.Fatalf("options = %d, want 2", len(got))
		}
		if got[0].Model != kimi || !got[0].Default || got[1].Model != deepseek || got[1].Default {
			t.Errorf("options = %+v, want Kimi (default) then DeepSeek", got)
		}
		if got[1].InputPer1KUSD != 0.00015 || got[1].OutputPer1KUSD != 0.0006 || !got[1].PricingKnown {
			t.Errorf("deepseek rates = %+v, want 0.00015 / 0.0006 per 1K", got[1])
		}
		if got[0].InputPer1KUSD != 0.00095 || got[0].OutputPer1KUSD != 0.004 {
			t.Errorf("kimi rates = %+v, want 0.00095 / 0.004 per 1K", got[0])
		}
	})

	t.Run("a deployment default off the list is offered first, by its id", func(t *testing.T) {
		got := NewChatModelService(newMemLLMCreds(), " anthropic/claude-haiku-4.5 ").options
		if len(got) != 3 {
			t.Fatalf("options = %d, want 3", len(got))
		}
		if got[0].Model != "anthropic/claude-haiku-4.5" || got[0].Label != "anthropic/claude-haiku-4.5" || !got[0].Default {
			t.Errorf("first option = %+v, want the env model labelled by its id, default", got[0])
		}
		for _, o := range got[1:] {
			if o.Default {
				t.Errorf("%s marked default; only LLM_MODEL is", o.Model)
			}
		}
	})

	t.Run("served options are a copy", func(t *testing.T) {
		svc := NewChatModelService(newMemLLMCreds(), kimi)
		s, err := svc.Get(context.Background(), "co-1")
		if err != nil {
			t.Fatal(err)
		}
		s.Options[0].Label = "mutated"
		if svc.options[0].Label != "Kimi K2.6" || ChatModels[0].Label != "Kimi K2.6" {
			t.Error("a caller's edit to a served option reached the service's list")
		}
	})
}

func TestChatModelGet(t *testing.T) {
	cases := []struct {
		name       string
		row        *domain.CompanyLLMCredential
		wantModel  string
		wantLocked bool
	}{
		{"no row runs LLM_MODEL", nil, kimi, false},
		{"a model-only row runs its model", primaryRow("co-1", func(r *domain.CompanyLLMCredential) { r.Model = deepseek }), deepseek, false},
		{"a row with no model keeps LLM_MODEL", primaryRow("co-1", func(r *domain.CompanyLLMCredential) { r.APIKeyEncrypted = []byte("k") }), kimi, true},
		{"own key is locked", primaryRow("co-1", func(r *domain.CompanyLLMCredential) { r.Model = "gpt-5"; r.APIKeyEncrypted = []byte("k") }), "gpt-5", true},
		{"own base URL is locked", primaryRow("co-1", func(r *domain.CompanyLLMCredential) { r.BaseURL = "https://llm.example" }), kimi, true},
		{"own interface is locked", primaryRow("co-1", func(r *domain.CompanyLLMCredential) { r.Interface = "anthropic" }), kimi, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemLLMCreds()
			if tc.row != nil {
				repo = newMemLLMCreds(tc.row)
			}
			got, err := NewChatModelService(repo, kimi).Get(context.Background(), "co-1")
			if err != nil {
				t.Fatal(err)
			}
			if got.Model != tc.wantModel || got.Locked != tc.wantLocked || got.DefaultModel != kimi {
				t.Errorf("Get = {model %q, locked %v, default %q}, want {%q, %v, %q}",
					got.Model, got.Locked, got.DefaultModel, tc.wantModel, tc.wantLocked, kimi)
			}
			if len(got.Options) != 2 {
				t.Errorf("options = %d, want 2", len(got.Options))
			}
		})
	}
}

// Q2. Each case starts from a row (or none), calls Set, and checks the error,
// the row left behind and how many writes it took.
func TestChatModelSet(t *testing.T) {
	boom := errors.New("db down")
	cases := []struct {
		name        string
		row         *domain.CompanyLLMCredential
		repo        func(*memLLMCreds)
		model       string
		wantErr     error
		wantAnyErr  bool
		wantRow     string // "" means no primary row afterwards; "same" means untouched
		wantModel   string // the setting's model on success
		wantUpserts int
		wantDeletes int
		wantLog     bool
	}{
		{
			name: "AC2: a listed model writes a model-only row", model: deepseek,
			wantRow: deepseek, wantModel: deepseek, wantUpserts: 1, wantLog: true,
		},
		{
			name: "surrounding space is not a different model", model: "  " + deepseek + " ",
			wantRow: deepseek, wantModel: deepseek, wantUpserts: 1, wantLog: true,
		},
		{
			name: "AC6: a model off the list is refused and nothing is written", model: "openai/gpt-5",
			row:     primaryRow("co-1", func(r *domain.CompanyLLMCredential) { r.Model = deepseek }),
			wantErr: domain.ErrModelNotOffered, wantRow: "same",
		},
		{
			name: "an empty model is not offered", model: "",
			wantErr: domain.ErrModelNotOffered, wantRow: "",
		},
		{
			name: "the casing of an id is part of it", model: "DeepSeek/DeepSeek-V4.1-Flash",
			wantErr: domain.ErrModelNotOffered, wantRow: "",
		},
		{
			name: "AC5: the default deletes the override", model: kimi,
			row:     primaryRow("co-1", func(r *domain.CompanyLLMCredential) { r.Model = deepseek }),
			wantRow: "", wantModel: kimi, wantDeletes: 1, wantLog: true,
		},
		{
			name: "R4: a row pinning the default is deleted too", model: kimi,
			row:     primaryRow("co-1", func(r *domain.CompanyLLMCredential) { r.Model = kimi }),
			wantRow: "", wantModel: kimi, wantDeletes: 1, wantLog: true,
		},
		{
			name: "the default on a workspace without a row writes nothing", model: kimi,
			wantRow: "", wantModel: kimi,
		},
		{
			name: "the model it already runs writes nothing", model: deepseek,
			row:     primaryRow("co-1", func(r *domain.CompanyLLMCredential) { r.Model = deepseek }),
			wantRow: "same", wantModel: deepseek,
		},
		{
			name: "AC8: own key is refused, row untouched", model: deepseek,
			row:     primaryRow("co-1", func(r *domain.CompanyLLMCredential) { r.Model = "gpt-5"; r.APIKeyEncrypted = []byte("sealed") }),
			wantErr: domain.ErrOwnProvider, wantRow: "same",
		},
		{
			name: "own base URL is refused", model: deepseek,
			row:     primaryRow("co-1", func(r *domain.CompanyLLMCredential) { r.BaseURL = "https://llm.example/v1" }),
			wantErr: domain.ErrOwnProvider, wantRow: "same",
		},
		{
			name: "own interface is refused", model: kimi,
			row:     primaryRow("co-1", func(r *domain.CompanyLLMCredential) { r.Interface = "anthropic" }),
			wantErr: domain.ErrOwnProvider, wantRow: "same",
		},
		{
			name: "a model off the list is refused before the row is read", model: "openai/gpt-5",
			repo:    func(m *memLLMCreds) { m.getErr = boom },
			wantErr: domain.ErrModelNotOffered, wantRow: "",
		},
		{
			name: "a failed read writes nothing", model: deepseek,
			repo:       func(m *memLLMCreds) { m.getErr = boom },
			wantAnyErr: true, wantRow: "",
		},
		{
			name: "a failed upsert is an error", model: deepseek,
			repo:       func(m *memLLMCreds) { m.upErr = boom },
			wantAnyErr: true, wantRow: "",
		},
		{
			name: "a failed delete is an error and the row stays", model: kimi,
			row:        primaryRow("co-1", func(r *domain.CompanyLLMCredential) { r.Model = deepseek }),
			repo:       func(m *memLLMCreds) { m.delErr = boom },
			wantAnyErr: true, wantRow: "same",
		},
		{
			name: "another tenant's row is refused, not overwritten", model: deepseek,
			repo: func(m *memLLMCreds) {
				m.foreign = primaryRow("co-2", func(r *domain.CompanyLLMCredential) { r.Model = kimi })
			},
			wantErr: domain.ErrUnauthorized, wantRow: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hook := logrustest.NewGlobal()
			defer hook.Reset()
			repo := newMemLLMCreds()
			var before domain.CompanyLLMCredential
			if tc.row != nil {
				before = *tc.row
				repo = newMemLLMCreds(tc.row)
			}
			if tc.repo != nil {
				tc.repo(repo)
			}

			got, err := NewChatModelService(repo, kimi).Set(context.Background(), "co-1", "u-admin", tc.model)

			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			case tc.wantAnyErr:
				if err == nil {
					t.Fatal("err = nil, want an error")
				}
			default:
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if got.Model != tc.wantModel {
					t.Errorf("setting model = %q, want %q", got.Model, tc.wantModel)
				}
			}

			row := repo.primary("co-1")
			switch tc.wantRow {
			case "":
				if row != nil {
					t.Errorf("primary row = %+v, want none", row)
				}
			case "same":
				if row == nil || row.Model != before.Model || row.BaseURL != before.BaseURL ||
					row.Interface != before.Interface || !bytes.Equal(row.APIKeyEncrypted, before.APIKeyEncrypted) {
					t.Errorf("primary row = %+v, want untouched %+v", row, before)
				}
			default:
				// Model only: our key, our base URL, our interface (Q4).
				if row == nil || row.Model != tc.wantRow || row.BaseURL != "" || row.Interface != "" || row.APIKeyEncrypted != nil {
					t.Errorf("primary row = %+v, want model-only %q", row, tc.wantRow)
				}
			}
			if repo.upserts != tc.wantUpserts || repo.deletes != tc.wantDeletes {
				t.Errorf("writes = %d upserts, %d deletes; want %d, %d",
					repo.upserts, repo.deletes, tc.wantUpserts, tc.wantDeletes)
			}

			var logged bool
			for _, e := range hook.AllEntries() {
				if e.Message != "chat_model_changed" {
					continue
				}
				logged = true
				if e.Data["company_id"] != "co-1" || e.Data["user_id"] != "u-admin" || e.Data["to"] == "" || e.Data["from"] == "" {
					t.Errorf("chat_model_changed fields = %v", e.Data)
				}
			}
			if logged != tc.wantLog {
				t.Errorf("chat_model_changed logged = %v, want %v", logged, tc.wantLog)
			}
		})
	}
}

// R10: the line says where the workspace came from, not only where it went.
func TestChatModelChangeLogsFromAndTo(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()
	repo := newMemLLMCreds()
	svc := NewChatModelService(repo, kimi)
	ctx := context.Background()

	if _, err := svc.Set(ctx, "co-1", "u-1", deepseek); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Set(ctx, "co-1", "u-2", kimi); err != nil {
		t.Fatal(err)
	}

	var pairs [][2]any
	for _, e := range hook.AllEntries() {
		if e.Message == "chat_model_changed" {
			pairs = append(pairs, [2]any{e.Data["from"], e.Data["to"]})
		}
	}
	want := [][2]any{{kimi, deepseek}, {deepseek, kimi}}
	if len(pairs) != 2 || pairs[0] != want[0] || pairs[1] != want[1] {
		t.Errorf("logged changes = %v, want %v", pairs, want)
	}
}

// AC4: one workspace's choice is its own.
func TestChatModelIsPerWorkspace(t *testing.T) {
	repo := newMemLLMCreds()
	svc := NewChatModelService(repo, kimi)
	ctx := context.Background()
	if _, err := svc.Set(ctx, "co-a", "u-1", deepseek); err != nil {
		t.Fatal(err)
	}
	b, err := svc.Get(ctx, "co-b")
	if err != nil {
		t.Fatal(err)
	}
	if b.Model != kimi {
		t.Errorf("workspace B runs %q, want %q", b.Model, kimi)
	}
}
