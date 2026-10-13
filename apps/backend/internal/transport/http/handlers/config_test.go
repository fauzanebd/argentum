package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/llmtenant"
)

// RIZ-97: the models route and the workspace chat model, through the real
// llmtenant.Resolver over a repository that remembers what it was told — so
// "GET shows what PUT saved" is a round trip and not two fixtures agreeing.
// The service's own rules are internal/app's tests; this is the mapping to
// status codes, the sentences, and what crosses the wire.

const (
	cfgKimi     = "moonshotai/kimi-k2.6"
	cfgDeepSeek = "deepseek/deepseek-v4.1-flash"
)

type handlerLLMCreds struct {
	mu     sync.Mutex
	rows   map[string]*domain.CompanyLLMCredential
	getErr error
}

func (m *handlerLLMCreds) GetByCompany(_ context.Context, companyID string) ([]*domain.CompanyLLMCredential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	var out []*domain.CompanyLLMCredential
	for _, r := range m.rows {
		if r.CompanyID == companyID {
			c := *r
			out = append(out, &c)
		}
	}
	return out, nil
}

func (m *handlerLLMCreds) Upsert(_ context.Context, c *domain.CompanyLLMCredential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c.UpdatedAt = time.Now()
	stored := *c
	m.rows[c.CompanyID+"|"+string(c.Tier)] = &stored
	return nil
}

func (m *handlerLLMCreds) Delete(_ context.Context, companyID string, tier domain.LLMTier) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, companyID+"|"+string(tier))
	return nil
}

func (m *handlerLLMCreds) primary(companyID string) *domain.CompanyLLMCredential {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rows[companyID+"|primary"]
}

type configHarness struct {
	r    *gin.Engine
	repo *handlerLLMCreds
}

func newConfigHarness(rows ...*domain.CompanyLLMCredential) *configHarness {
	gin.SetMode(gin.TestMode)
	repo := &handlerLLMCreds{rows: map[string]*domain.CompanyLLMCredential{}}
	for _, r := range rows {
		repo.rows[r.CompanyID+"|"+string(r.Tier)] = r
	}
	cfg := &config.Config{
		LLMModel:      cfgKimi,
		LightLLMModel: "openai/gpt-5-mini",
		// Unset on purpose: the classifier row must report the light model.
		ClassifierModel: "",
	}
	h := &configHarness{r: gin.New(), repo: repo}
	h.r.Use(func(c *gin.Context) {
		c.Set("company_id", c.GetHeader("X-Company"))
		c.Set("user_id", "u-admin")
	})
	// No cipher: no row in these tests carries a key the resolver would
	// decrypt. The locked case uses a base URL, which locks just the same.
	resolver := llmtenant.NewResolver(repo, nil, cfg)
	NewConfigHandler(cfg, resolver, app.NewChatModelService(repo, cfg.LLMModel)).Register(h.r.Group("/api"))
	return h
}

func (h *configHarness) do(t *testing.T, method, path, company, body string) (int, []byte) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Company", company)
	w := httptest.NewRecorder()
	h.r.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

func (h *configHarness) models(t *testing.T, company string) ModelsResponse {
	t.Helper()
	code, body := h.do(t, http.MethodGet, "/api/config/models", company, "")
	if code != http.StatusOK {
		t.Fatalf("GET /config/models = %d %s", code, body)
	}
	var out ModelsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Q3 / AC1: a fresh workspace runs LLM_MODEL and is offered both models.
func TestModelsReportsAFreshWorkspace(t *testing.T) {
	got := newConfigHarness().models(t, "co-1")

	if got.Primary.Model != cfgKimi || !got.Primary.PricingKnown || got.Primary.InputPer1KUSD != 0.00095 {
		t.Errorf("primary = %+v, want kimi at its rate", got.Primary)
	}
	if got.Classifier.Model != "openai/gpt-5-mini" {
		t.Errorf("classifier = %q, want the light model when unset", got.Classifier.Model)
	}
	cm := got.ChatModel
	if cm == nil {
		t.Fatal("chat_model absent")
	}
	if cm.Model != cfgKimi || cm.DefaultModel != cfgKimi || cm.Locked || len(cm.Options) != 2 {
		t.Errorf("chat_model = %+v, want kimi, default kimi, unlocked, 2 options", cm)
	}
	if o := cm.Options[1]; o.Model != cfgDeepSeek || o.Label != "DeepSeek V4.1 Flash" || o.InputPer1KUSD != 0.00015 || o.OutputPer1KUSD != 0.0006 {
		t.Errorf("second option = %+v", o)
	}
}

// Q4 / Q8 / AC2 / AC5 / AC10: PUT, then GET reports it — in chat_model and in
// primary, which is what the chat header reads — then back to the default.
func TestChatModelRoundTrip(t *testing.T) {
	h := newConfigHarness()

	code, body := h.do(t, http.MethodPut, "/api/config/chat-model", "co-1", `{"model":"`+cfgDeepSeek+`"}`)
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	var saved domain.ChatModelSetting
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Model != cfgDeepSeek || len(saved.Options) != 2 {
		t.Errorf("PUT answered %+v, want the new setting", saved)
	}
	row := h.repo.primary("co-1")
	if row == nil || row.Model != cfgDeepSeek || row.Interface != "" || row.BaseURL != "" || row.APIKeyEncrypted != nil {
		t.Fatalf("row = %+v, want model only", row)
	}

	got := h.models(t, "co-1")
	if got.Primary.Model != cfgDeepSeek || got.Primary.InputPer1KUSD != 0.00015 || got.ChatModel.Model != cfgDeepSeek {
		t.Errorf("after PUT: primary %+v, chat_model %q; want deepseek in both", got.Primary, got.ChatModel.Model)
	}
	// AC4: another workspace is untouched.
	if other := h.models(t, "co-2"); other.Primary.Model != cfgKimi || other.ChatModel.Model != cfgKimi {
		t.Errorf("co-2 runs %q / %q, want kimi", other.Primary.Model, other.ChatModel.Model)
	}

	code, body = h.do(t, http.MethodPut, "/api/config/chat-model", "co-1", `{"model":"`+cfgKimi+`"}`)
	if code != http.StatusOK {
		t.Fatalf("PUT default = %d %s", code, body)
	}
	if row := h.repo.primary("co-1"); row != nil {
		t.Errorf("row after choosing the default = %+v, want none", row)
	}
	if got := h.models(t, "co-1"); got.Primary.Model != cfgKimi || got.ChatModel.Model != cfgKimi {
		t.Errorf("after default: %q / %q, want kimi", got.Primary.Model, got.ChatModel.Model)
	}
}

func TestChatModelRefusals(t *testing.T) {
	deepseekRow := func() *domain.CompanyLLMCredential {
		return &domain.CompanyLLMCredential{CompanyID: "co-1", Tier: domain.LLMTierPrimary, Model: cfgDeepSeek}
	}
	cases := []struct {
		name     string
		row      *domain.CompanyLLMCredential
		body     string
		wantCode int
		wantErr  string
		wantKind string
		wantRow  string
	}{
		{
			name: "AC6: not offered", row: deepseekRow(), body: `{"model":"openai/gpt-5"}`,
			wantCode: http.StatusBadRequest, wantKind: "model_not_offered",
			wantErr: "openai/gpt-5 is not offered on this workspace", wantRow: cfgDeepSeek,
		},
		{
			name: "no model", body: `{}`,
			wantCode: http.StatusBadRequest, wantKind: "model_not_offered", wantErr: "choose a chat model",
		},
		{
			name: "a body that does not parse", body: `{"model":`,
			wantCode: http.StatusBadRequest, wantKind: "model_not_offered", wantErr: "choose a chat model",
		},
		{
			name: "AC8: own provider",
			row: &domain.CompanyLLMCredential{
				CompanyID: "co-1", Tier: domain.LLMTierPrimary, Model: "gpt-5", BaseURL: "https://llm.example/v1",
			},
			body:     `{"model":"` + cfgDeepSeek + `"}`,
			wantCode: http.StatusConflict, wantKind: "own_provider",
			wantErr: "This workspace runs on its own model provider. Contact support to change its chat model.",
			wantRow: "gpt-5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var h *configHarness
			if tc.row != nil {
				h = newConfigHarness(tc.row)
			} else {
				h = newConfigHarness()
			}
			code, body := h.do(t, http.MethodPut, "/api/config/chat-model", "co-1", tc.body)
			if code != tc.wantCode {
				t.Fatalf("PUT = %d %s, want %d", code, body, tc.wantCode)
			}
			var e struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}
			if err := json.Unmarshal(body, &e); err != nil {
				t.Fatal(err)
			}
			if e.Code != tc.wantKind || e.Error != tc.wantErr {
				t.Errorf("body = %+v, want code %q error %q", e, tc.wantKind, tc.wantErr)
			}
			row := h.repo.primary("co-1")
			switch {
			case tc.wantRow == "" && row != nil:
				t.Errorf("row = %+v, want none", row)
			case tc.wantRow != "" && (row == nil || row.Model != tc.wantRow):
				t.Errorf("row = %+v, want model %q untouched", row, tc.wantRow)
			}
		})
	}
}

// AC8 on the read side: the card is told the workspace is locked, and the
// model it reports is the row's.
func TestModelsReportsALockedWorkspace(t *testing.T) {
	h := newConfigHarness(&domain.CompanyLLMCredential{
		CompanyID: "co-1", Tier: domain.LLMTierPrimary, Model: "gpt-5", Interface: "openai", BaseURL: "https://llm.example/v1",
	})
	got := h.models(t, "co-1")
	if !got.ChatModel.Locked || got.ChatModel.Model != "gpt-5" || got.Primary.Model != "gpt-5" {
		t.Errorf("chat_model = %+v, primary = %q; want locked on gpt-5", got.ChatModel, got.Primary.Model)
	}
}

// A workspace whose row cannot be read is told so, rather than told it runs
// LLM_MODEL — the misreport RIZ-97 removed.
func TestModelsFailsRatherThanReportingEnv(t *testing.T) {
	h := newConfigHarness()
	h.repo.getErr = errors.New("db down")
	code, _ := h.do(t, http.MethodGet, "/api/config/models", "co-1", "")
	if code != http.StatusInternalServerError {
		t.Errorf("GET = %d, want 500", code)
	}
	code, _ = h.do(t, http.MethodPut, "/api/config/chat-model", "co-1", `{"model":"`+cfgDeepSeek+`"}`)
	if code != http.StatusInternalServerError {
		t.Errorf("PUT = %d, want 500", code)
	}
}

// Without a database (cmd/api's router tests) the read answers from env as it
// did before RIZ-97 and without a chat_model block, and the write is a 503.
func TestConfigHandlerWithoutServices(t *testing.T) {
	r := gin.New()
	NewConfigHandler(&config.Config{LLMModel: cfgKimi}, nil, nil).Register(r.Group("/api"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/config/models", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d", w.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["chat_model"]; ok {
		t.Error("chat_model present without a service; an old dashboard reads that as a picker")
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/config/chat-model", strings.NewReader(`{"model":"`+cfgDeepSeek+`"}`)))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("PUT = %d, want 503", w.Code)
	}
}
