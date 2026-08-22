package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/skill"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// LoadSkillTool opens one written procedure by name (T-K4).
//
// This is the second half of progressive disclosure: the index in the system
// prompt carries `name — when_to_use` and no steps, and this is how the steps
// arrive. The body comes back inside `skill.Frame`, which is the deliberate
// exception to T-H8's rule — see that package for the authorship argument, and
// `trustedResults` in untrusted.go for the one-line version.
//
// **A skill grants nothing.** Loading one cannot widen a scope: a procedure
// that says "query the HR database" on an agent scoped away from it produces a
// refused `run_sql` and a confused turn, which is what T-S2 established for the
// persona. The only thing this tool decides is whether the model may *read* a
// procedure the workspace wrote.
type LoadSkillTool struct {
	repo domain.SkillRepository
}

// NewLoadSkillTool wires the tool. A nil repository registers the name and
// answers "not configured" — the metric tools' pattern, and it matters for the
// same reason: a deployment that has not enabled skills must not have a
// different tool list from one that has, or an agent allowlist written on one
// stops resolving on the other.
func NewLoadSkillTool(repo domain.SkillRepository) *LoadSkillTool {
	return &LoadSkillTool{repo: repo}
}

func (t *LoadSkillTool) Name() string { return "load_skill" }

func (t *LoadSkillTool) Description() string {
	return "Read one of this workspace's written procedures in full, by its exact name. " +
		"The system prompt lists the procedures that exist and when each applies, but not their steps — " +
		"this is how you read the steps. Call it when a listed procedure fits what is being asked, then follow it. " +
		"Do not guess names: only the ones in that list exist."
}

func (t *LoadSkillTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"name": {
			Type:        "string",
			Description: "The exact procedure name as it appears in the list in your instructions.",
			Required:    true,
		},
	}
}

func (t *LoadSkillTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

// Execute resolves the name and returns the framed body.
//
// **Every refusal is a result, not a Go error**, which is `halfWindow`'s
// finding applied before it can cost a turn: the 2026-08-16 eval run caught
// deepseek answering a Go error by re-sending the identical call five more
// times until the iteration budget ended the turn. A model that mistypes a
// skill name would do exactly that, so each refusal below names what went
// wrong, lists the names that do exist where that is safe, and says not to
// repeat the call unchanged.
//
// The Go error path is reserved for the two cases the model cannot act on at
// all: no tenant in context, and a repository failure.
func (t *LoadSkillTool) Execute(ctx context.Context, input string) (string, error) {
	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return "", fmt.Errorf("no tenant in context")
	}
	if t.repo == nil {
		return refusal("skills are not configured on this deployment",
			"Answer from the tools you have. Do not call load_skill again this turn."), nil
	}

	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return refusal("load_skill takes one argument: name",
			`Call it as {"name":"<the exact name from the list in your instructions>"}.`), nil
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return refusal("no procedure name was given",
			"Send the exact name as it appears in the list in your instructions."), nil
	}

	row, err := t.repo.GetByName(ctx, companyID, name)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return "", fmt.Errorf("load skill: %w", err)
	}

	// **Three of the four refusals are deliberately the same sentence.** A
	// name that does not exist, a name belonging to another company, and a
	// name this agent is not bound to all answer "no procedure by that name" —
	// because the alternative is a tool that tells a model which uuids and
	// which names exist elsewhere. A 404 is not a directory, and the roster
	// and the skills CRUD both already answer this way.
	//
	// A *disabled* skill is the exception and gets its own sentence: it is the
	// tenant's own procedure, temporarily off, and "this exists and is turned
	// off" is something the model can usefully say back to the person asking.
	if err != nil || row == nil || !t.offeredToThisTurn(ctx, row) {
		return refusal(fmt.Sprintf("no procedure named %q is available to you", name),
			"Use one of the names listed in your instructions exactly as written, or answer without a procedure. "+
				"Do not send this call again with the same name."+t.availableNames(ctx, companyID)), nil
	}
	if !row.Enabled {
		return refusal(fmt.Sprintf("the procedure %q exists but is currently switched off by this workspace", row.Name),
			"Do not use it. Answer from the tools you have, and say that this procedure is disabled only if the "+
				"person asked about the procedure itself."), nil
	}

	// The body, framed. Encoded with HTML escaping OFF: json.Marshal escapes
	// `<` and `>` by default, which would turn every marker into its `\u003c`
	// spelling — a boundary the system prompt names and the model never sees.
	// That is the defect T-H8 found in its own fence, three weeks after a live
	// gate had signed the feature off.
	out := map[string]any{
		"name":  row.Name,
		"skill": skill.Frame(row.Name, row.Body),
		// row_count is what agentbudget reads to decide whether the turn
		// retrieved anything. A procedure is instruction rather than evidence,
		// so it is not a row: a turn that loaded a skill and produced no figure
		// has still retrieved nothing.
		"row_count": 0,
	}
	return encodeUnescaped(out), nil
}

// offeredToThisTurn applies the agent binding — the same rule the index
// composes with, applied again here because the index is a *hint* and this is
// the read. A model that remembers a name from an earlier turn on a different
// agent must not be able to open it.
//
// An unscoped turn (the eval harness, a channel with no agent row) carries no
// binding and is offered everything, which is what every other scope check in
// this tree does with an empty scope.
func (t *LoadSkillTool) offeredToThisTurn(ctx context.Context, row *domain.Skill) bool {
	scope := agentscope.FromContext(ctx)
	if len(scope.SkillIDs) == 0 {
		return true
	}
	for _, id := range scope.SkillIDs {
		if id == row.ID {
			return true
		}
	}
	return false
}

// availableNames lists what this agent *can* open, so a mistyped name is
// recoverable in one call rather than in five.
//
// Only the names this turn is already allowed to see — they are in its system
// prompt already, so repeating them leaks nothing. It returns the empty string
// when there is nothing to list, because "here are the procedures: none" reads
// as a system fault rather than as an empty workspace.
func (t *LoadSkillTool) availableNames(ctx context.Context, companyID string) string {
	rows, err := t.repo.ListEnabledForIndex(ctx, companyID)
	if err != nil || len(rows) == 0 {
		return ""
	}
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		if t.offeredToThisTurn(ctx, r) {
			names = append(names, strconv.Quote(r.Name))
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return " The procedures available to you are: " + strings.Join(names, ", ") + "."
}

// refusal is the shape every recoverable failure here takes: a sentence saying
// what happened and a note saying what to do instead.
func refusal(what, note string) string {
	return encodeUnescaped(map[string]any{
		"error":     what,
		"note":      note,
		"row_count": 0,
	})
}

// encodeUnescaped writes JSON with HTML escaping off.
//
// Every result this file produces goes through it, including the refusals: the
// framed body is the reason, and a refusal encoded differently from a success
// is a second encoder to keep in step with the first. search_documents makes
// the same choice for the same reason, and T-H8's own fence is what happens
// when only one of two paths remembers.
func encodeUnescaped(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		// Unreachable for the maps this file builds, and a Go error here would
		// be the one shape the model cannot act on. An empty object is at
		// least parseable.
		return `{"error":"could not encode the result","row_count":0}`
	}
	return strings.TrimRight(buf.String(), "\n")
}
