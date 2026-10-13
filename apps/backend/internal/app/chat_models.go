package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// ChatModels is the allow-list a workspace admin picks the chat model from
// (RIZ-97). Only Model, Label and Hint are set here; Default and the rates are
// filled in by NewChatModelService, from LLM_MODEL and the pricing table, so
// neither has a second copy that could drift.
//
// In code rather than free text or env, on purpose. A model id with no row in
// modelPricing bills at DefaultPricing — five times Kimi's input rate,
// thirty-three times DeepSeek's — and credits refuse turns off that number; a
// model without tool support cannot run a turn at all. TestEveryChatModelIsPriced holds the
// first, and an entry joins this list only after a paired `make eval -models`
// run against Kimi (docs/coverage/chat-model.md), which is what holds the
// second.
var ChatModels = []domain.ChatModelOption{
	{
		Model: "moonshotai/kimi-k2.6",
		Label: "Kimi K2.6",
		Hint:  "The model Argentum is tuned and tested on.",
	},
	{
		Model: "deepseek/deepseek-v4.1-flash",
		Label: "DeepSeek V4.1 Flash",
		Hint:  "About a sixth of the cost per answer. Check a few of your usual questions after switching.",
	},
}

// ChatModelService reads and writes a workspace's chat model: the `model`
// column of its `primary` row in company_llm_credentials.
//
// It writes nothing else on that row. The resolver merges a row per field, so
// a row holding only a model keeps our key, our base URL and our interface —
// which is exactly "this workspace runs DeepSeek on Argentum's account" — and
// credits keep counting it against our balance, because hasOwnPrimaryKey
// counts a row as bring-your-own only when it carries a key.
//
// No cache is touched here. Upsert bumps updated_at, which is the resolver's
// version, so every process's ClientCache rebuilds the tenant's client on its
// next turn; Delete makes Resolve return the env profile, whose version
// differs from any row's. That is the whole of "effective from the next
// message, with no restart".
type ChatModelService struct {
	repo         domain.CompanyLLMCredentialRepository
	defaultModel string
	options      []domain.ChatModelOption
}

// NewChatModelService wires it. defaultModel is LLM_MODEL. When it is not on
// the allow-list — a local stack on a Claude or GPT key — it is offered first,
// labelled by its id, so the picker can always return a workspace to what the
// deployment runs. Production runs Kimi, so there the list is exactly two.
func NewChatModelService(repo domain.CompanyLLMCredentialRepository, defaultModel string) *ChatModelService {
	defaultModel = strings.TrimSpace(defaultModel)
	opts := make([]domain.ChatModelOption, 0, len(ChatModels)+1)
	listed := false
	for _, o := range ChatModels {
		if o.Model == defaultModel {
			listed = true
		}
	}
	if !listed && defaultModel != "" {
		opts = append(opts, domain.ChatModelOption{Model: defaultModel, Label: defaultModel})
	}
	opts = append(opts, ChatModels...)
	for i := range opts {
		opts[i].Default = opts[i].Model == defaultModel
		if p, ok := LookupModelPricing(opts[i].Model); ok {
			opts[i].InputPer1KUSD = p.InputCostPer1K
			opts[i].OutputPer1KUSD = p.OutputCostPer1K
			opts[i].PricingKnown = true
		}
	}
	return &ChatModelService{repo: repo, defaultModel: defaultModel, options: opts}
}

// DefaultModel is LLM_MODEL as the service compares against it.
func (s *ChatModelService) DefaultModel() string { return s.defaultModel }

// Get reports the workspace's chat model and what it may choose between.
func (s *ChatModelService) Get(ctx context.Context, companyID string) (*domain.ChatModelSetting, error) {
	row, err := s.primaryRow(ctx, companyID)
	if err != nil {
		return nil, err
	}
	return s.setting(row), nil
}

// Set makes model the workspace's chat model and returns the new setting.
//
// The order of the refusals is the contract: a model off the list is refused
// before the row is read, so a 400 never depends on the workspace's state, and
// a workspace on its own provider is refused before anything is written, so a
// 409 leaves its row exactly as support left it.
func (s *ChatModelService) Set(ctx context.Context, companyID, userID, model string) (*domain.ChatModelSetting, error) {
	model = strings.TrimSpace(model)
	if !s.offered(model) {
		return nil, domain.ErrModelNotOffered
	}
	row, err := s.primaryRow(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if ownProvider(row) {
		return nil, domain.ErrOwnProvider
	}
	from := s.resolved(row)

	// Choosing the default removes the override instead of pinning it (R4):
	// a row saying "kimi-k2.6" would keep this workspace on Kimi after the
	// deployment moved on, which is not what an admin who picked "default"
	// asked for. A write that would change nothing is skipped, so saving the
	// same model twice does not rebuild every process's client for nothing.
	wrote := false
	if model == s.defaultModel {
		if row != nil {
			if err := s.repo.Delete(ctx, companyID, domain.LLMTierPrimary); err != nil {
				return nil, fmt.Errorf("delete chat model override: %w", err)
			}
			row, wrote = nil, true
		}
	} else if row == nil || strings.TrimSpace(row.Model) != model {
		row = &domain.CompanyLLMCredential{CompanyID: companyID, Tier: domain.LLMTierPrimary, Model: model}
		if err := s.repo.Upsert(ctx, row); err != nil {
			return nil, fmt.Errorf("save chat model override: %w", err)
		}
		wrote = true
	}

	if wrote {
		// The one record of who changed it (R10). No audit table: the ticket
		// scoped that out, and this line carries what an incident needs to
		// explain a workspace's answers changing character overnight.
		logrus.WithFields(logrus.Fields{
			"company_id": companyID,
			"user_id":    userID,
			"from":       from,
			"to":         model,
		}).Info("chat_model_changed")
	}
	return s.setting(row), nil
}

func (s *ChatModelService) offered(model string) bool {
	if model == "" {
		return false
	}
	for _, o := range s.options {
		if o.Model == model {
			return true
		}
	}
	return false
}

// primaryRow is the workspace's primary-tier row, or nil. The company check
// is llmtenant.Resolver's, repeated: a repository that returned another
// tenant's row here would let one admin's Save overwrite a stranger's model.
func (s *ChatModelService) primaryRow(ctx context.Context, companyID string) (*domain.CompanyLLMCredential, error) {
	if companyID == "" {
		return nil, fmt.Errorf("no tenant in context: cannot read chat model")
	}
	rows, err := s.repo.GetByCompany(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("load llm credentials: %w", err)
	}
	for _, row := range rows {
		if row.CompanyID != companyID {
			return nil, domain.ErrUnauthorized
		}
		if row.Tier == domain.LLMTierPrimary {
			return row, nil
		}
	}
	return nil, nil
}

// resolved mirrors llmtenant.Resolver.merge for the one field read here: the
// row's model when set, LLM_MODEL when the row is absent or leaves it empty.
func (s *ChatModelService) resolved(row *domain.CompanyLLMCredential) string {
	if row != nil {
		if m := strings.TrimSpace(row.Model); m != "" {
			return m
		}
	}
	return s.defaultModel
}

func (s *ChatModelService) setting(row *domain.CompanyLLMCredential) *domain.ChatModelSetting {
	opts := make([]domain.ChatModelOption, len(s.options))
	copy(opts, s.options)
	return &domain.ChatModelSetting{
		Model:        s.resolved(row),
		DefaultModel: s.defaultModel,
		Locked:       ownProvider(row),
		Options:      opts,
	}
}

// ownProvider reports whether the row carries anything beyond a model. Wider
// than credits' hasOwnPrimaryKey on purpose: a base URL or interface without a
// key still points this workspace somewhere support set up by hand, and the
// picker's two models are only known to work on ours.
func ownProvider(row *domain.CompanyLLMCredential) bool {
	if row == nil {
		return false
	}
	return len(row.APIKeyEncrypted) > 0 ||
		strings.TrimSpace(row.BaseURL) != "" ||
		strings.TrimSpace(row.Interface) != ""
}
