package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/guardrails"
	"github.com/fauzanebd/argentum/internal/skill"
	"github.com/fauzanebd/argentum/internal/taint"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

type skillRepoStub struct {
	rows    map[string]*domain.Skill
	listErr error
}

func newSkillRepoStub(rows ...*domain.Skill) *skillRepoStub {
	s := &skillRepoStub{rows: map[string]*domain.Skill{}}
	for _, r := range rows {
		s.rows[strings.ToLower(r.CompanyID+"/"+r.Name)] = r
	}
	return s
}

func (s *skillRepoStub) Create(context.Context, *domain.Skill) error { return nil }
func (s *skillRepoStub) GetByID(context.Context, string, string) (*domain.Skill, error) {
	return nil, domain.ErrNotFound
}

func (s *skillRepoStub) GetByName(_ context.Context, companyID, name string) (*domain.Skill, error) {
	r, ok := s.rows[strings.ToLower(companyID+"/"+name)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return r, nil
}

func (s *skillRepoStub) ListByCompany(context.Context, string) ([]*domain.Skill, error) {
	return nil, nil
}

func (s *skillRepoStub) ListEnabledRankedForIndex(ctx context.Context, companyID string, _ []float32) ([]*domain.Skill, error) {
	return s.ListEnabledForIndex(ctx, companyID)
}

func (s *skillRepoStub) ListUnembedded(context.Context, string) ([]*domain.Skill, error) {
	return nil, nil
}

func (s *skillRepoStub) SetEmbedding(context.Context, string, *domain.Skill, []float32, string) error {
	return nil
}

func (s *skillRepoStub) ListEnabledForIndex(_ context.Context, companyID string) ([]*domain.Skill, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := []*domain.Skill{}
	for _, r := range s.rows {
		if r.CompanyID == companyID && r.Enabled {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *skillRepoStub) Update(context.Context, *domain.Skill) error         { return nil }
func (s *skillRepoStub) Delete(context.Context, string, string) error        { return nil }
func (s *skillRepoStub) CountByCompany(context.Context, string) (int, error) { return 0, nil }
func (s *skillRepoStub) SetAgentBinding(context.Context, string, string, []string) error {
	return nil
}
func (s *skillRepoStub) AgentBinding(context.Context, string, string) ([]string, error) {
	return nil, nil
}

func skillRow(companyID, id, name string, enabled bool) *domain.Skill {
	return &domain.Skill{
		ID: id, CompanyID: companyID, Name: name, Enabled: enabled,
		WhenToUse: "The user asks for the weekly summary.",
		Body:      "1. Query fact_sales.\n2. Exclude cancelled orders.",
	}
}

func skillTurnCtx(companyID string, scope agentscope.Scope) context.Context {
	return agentscope.WithScope(tenantctx.WithCompanyID(context.Background(), companyID), scope)
}

func decodeSkillResult(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, out)
	}
	return m
}

// **The four refusals, and every one of them is a result rather than a Go
// error.** halfWindow's finding, applied before it can cost a turn: a Go error
// is what deepseek answered by re-sending the identical call five more times
// until the iteration budget ended the turn.
func TestLoadSkillRefusalsAreResultsTheModelCanActOn(t *testing.T) {
	mine := skillRow("co-1", "s1", "Weekly report", true)
	theirs := skillRow("co-2", "s2", "Their procedure", true)
	off := skillRow("co-1", "s3", "Being revised", false)
	unbound := skillRow("co-1", "s4", "Not for this agent", true)
	tool := NewLoadSkillTool(newSkillRepoStub(mine, theirs, off, unbound))

	cases := []struct {
		name       string
		scope      agentscope.Scope
		arg        string
		wantPhrase string
	}{
		{"unknown name", agentscope.Scope{}, `{"name":"No such procedure"}`, "no procedure named"},
		{"another company's skill", agentscope.Scope{}, `{"name":"Their procedure"}`, "no procedure named"},
		{"outside this agent's binding", agentscope.Scope{SkillIDs: []string{"s1"}}, `{"name":"Not for this agent"}`, "no procedure named"},
		{"a disabled skill", agentscope.Scope{}, `{"name":"Being revised"}`, "switched off"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tool.Execute(skillTurnCtx("co-1", tc.scope), tc.arg)
			if err != nil {
				t.Fatalf("the refusal came back as a Go error, which is the shape that loops: %v", err)
			}
			m := decodeSkillResult(t, out)
			msg, _ := m["error"].(string)
			if !strings.Contains(msg, tc.wantPhrase) {
				t.Errorf("error = %q, want it to contain %q", msg, tc.wantPhrase)
			}
			note, _ := m["note"].(string)
			if note == "" {
				t.Error("the refusal carries no note; a model told what went wrong and not what to do next repeats the call")
			}
			// A refusal is not evidence, and agentbudget reads this field to
			// decide whether the turn retrieved anything.
			if m["row_count"] != float64(0) {
				t.Errorf("row_count = %v, want 0", m["row_count"])
			}
			if _, leaked := m["skill"]; leaked {
				t.Error("a refusal carried a body")
			}
		})
	}
}

// A 404 is not a directory. Three of the four refusals say the same sentence on
// purpose: a tool that distinguished "does not exist" from "belongs to somebody
// else" would answer the question of which names exist in other workspaces.
func TestLoadSkillDoesNotRevealAnotherCompanysNames(t *testing.T) {
	theirs := skillRow("co-2", "s2", "Acquisition due diligence", true)
	tool := NewLoadSkillTool(newSkillRepoStub(theirs))

	out, err := tool.Execute(skillTurnCtx("co-1", agentscope.Scope{}), `{"name":"Acquisition due diligence"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	m := decodeSkillResult(t, out)
	unknown, _ := tool.Execute(skillTurnCtx("co-1", agentscope.Scope{}), `{"name":"Something else entirely"}`)
	other := decodeSkillResult(t, unknown)

	// Both echo back the name the *model* sent, which leaks nothing — it sent
	// it. What must be identical is everything else: strip the quoted name and
	// the two refusals have to be the same sentence, or the difference between
	// them is an oracle for which names exist in other workspaces.
	strip := func(v any, name string) string {
		return strings.ReplaceAll(v.(string), name, "<name>")
	}
	got := strip(m["error"], "Acquisition due diligence")
	want := strip(other["error"], "Something else entirely")
	if got != want {
		t.Errorf("a cross-tenant name got a different refusal from an unknown one:\n%s\nvs\n%s", got, want)
	}
	if strings.Contains(m["note"].(string), "Acquisition") && strings.Contains(m["note"].(string), "available to you are") {
		t.Error("the note listed another company's procedure back to the caller")
	}
}

// A mistyped name has to be recoverable in one call, not five — so the refusal
// lists what this agent can actually open.
func TestLoadSkillListsTheNamesThatDoExist(t *testing.T) {
	tool := NewLoadSkillTool(newSkillRepoStub(
		skillRow("co-1", "s1", "Weekly report", true),
		skillRow("co-1", "s2", "Month end close", true),
		skillRow("co-2", "s3", "Somebody else's", true),
	))

	out, _ := tool.Execute(skillTurnCtx("co-1", agentscope.Scope{}), `{"name":"Weekly Reprot"}`)
	note, _ := decodeSkillResult(t, out)["note"].(string)
	for _, want := range []string{"Weekly report", "Month end close"} {
		if !strings.Contains(note, want) {
			t.Errorf("the refusal does not offer %q: %s", want, note)
		}
	}
	if strings.Contains(note, "Somebody else's") {
		t.Errorf("the refusal listed another company's procedure: %s", note)
	}
}

// The body arrives framed and unfenced. Both halves matter: framed, or the
// model has no reason to treat it as instruction; unfenced, or it has been told
// its own workspace's procedure is third-party data.
func TestLoadSkillReturnsTheBodyFramedAndUnfenced(t *testing.T) {
	row := skillRow("co-1", "s1", "Weekly report", true)
	tool := NewLoadSkillTool(newSkillRepoStub(row))

	out, err := tool.Execute(skillTurnCtx("co-1", agentscope.Scope{}), `{"name":"Weekly report"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if guardrails.IsFenced(out) {
		t.Errorf("the result is fenced:\n%s", out)
	}
	if !strings.Contains(out, skill.FrameOpen) {
		t.Errorf("the result carries no frame:\n%s", out)
	}
	// The markers must be in the encoded bytes as written, not as `<…`.
	// T-H8's defect, at the one seam this feature adds.
	if strings.Contains(out, "\\u003c") {
		t.Errorf("the frame was HTML-escaped on the way out:\n%s", out)
	}
	body, _ := decodeSkillResult(t, out)["skill"].(string)
	if !strings.Contains(body, "Exclude cancelled orders") {
		t.Errorf("the body did not survive the round trip: %s", body)
	}
	// Case-insensitive resolution: the name is prose in a prompt, and a model
	// retyping it in another case has named the same procedure.
	if _, err := tool.Execute(skillTurnCtx("co-1", agentscope.Scope{}), `{"name":"WEEKLY REPORT"}`); err != nil {
		t.Fatalf("a differently-cased name errored: %v", err)
	}
}

// **The assertion the whole feature rests on**, and it is explicit because the
// claim is that this one result is different: a loaded procedure must not mark
// the turn's taint tracker. If it did, every turn that read a procedure would
// be treated as having read untrusted content — and under T-H9 that gates the
// actions the procedure exists to describe.
func TestLoadSkillDoesNotTaintTheTurn(t *testing.T) {
	row := skillRow("co-1", "s1", "Weekly report", true)
	tracker := taint.New()
	ctx := taint.With(skillTurnCtx("co-1", agentscope.Scope{}), tracker)

	// Through the decorator the agent's registry actually wraps it in, not the
	// bare tool: the question is what the *turn* records, and the decorator is
	// where that is decided.
	tool := MarkUntrustedReads(NewLoadSkillTool(newSkillRepoStub(row)))
	if _, err := tool.Execute(ctx, `{"name":"Weekly report"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if kinds := tracker.Kinds(); len(kinds) != 0 {
		t.Errorf("load_skill tainted the turn: %v", kinds)
	}

	// The control, so the assertion is known to discriminate: a tool that is
	// not in trustedResults does taint it.
	other := MarkUntrustedReads(&fixedTool{name: "run_sql", out: `{"rows":[]}`})
	if _, err := other.Execute(ctx, "{}"); err != nil {
		t.Fatalf("control tool: %v", err)
	}
	if kinds := tracker.Kinds(); len(kinds) == 0 {
		t.Error("the control did not taint the turn either; this test proves nothing")
	}
}

// And the fence decorator must leave it alone too — a framed procedure wrapped
// in an untrusted fence is a procedure the model has been told to ignore.
func TestLoadSkillIsNotFencedByTheDecorator(t *testing.T) {
	row := skillRow("co-1", "s1", "Weekly report", true)
	tool := FenceResults(NewLoadSkillTool(newSkillRepoStub(row)))

	out, err := tool.Execute(skillTurnCtx("co-1", agentscope.Scope{}), `{"name":"Weekly report"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if guardrails.IsFenced(out) {
		t.Errorf("the fence decorator wrapped a workspace procedure:\n%s", out)
	}
	if !strings.Contains(out, skill.FrameOpen) {
		t.Errorf("the frame did not survive the decorator:\n%s", out)
	}
}

// A deployment with no repository still registers the tool, and answers
// something the model can act on rather than panicking.
func TestLoadSkillWithoutARepositoryRefusesCleanly(t *testing.T) {
	tool := NewLoadSkillTool(nil)
	out, err := tool.Execute(skillTurnCtx("co-1", agentscope.Scope{}), `{"name":"Anything"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if msg, _ := decodeSkillResult(t, out)["error"].(string); !strings.Contains(msg, "not configured") {
		t.Errorf("error = %q", msg)
	}
}

// fixedTool is the control for the taint assertion: a tool that is not in
// trustedResults and returns content, so the decorator marks the turn.
type fixedTool struct {
	name string
	out  string
}

func (f *fixedTool) Name() string        { return f.name }
func (f *fixedTool) Description() string { return "" }
func (f *fixedTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{}
}
func (f *fixedTool) Run(ctx context.Context, in string) (string, error) {
	return f.Execute(ctx, in)
}
func (f *fixedTool) Execute(context.Context, string) (string, error) { return f.out, nil }
