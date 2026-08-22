package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/skill"
)

// T-K3 on the composition side: where the index lands, and — the arm that
// decides whether this can ship to existing tenants — that it does not exist at
// all for a company with no skills.

func skillIndexBlock(t *testing.T, names ...string) string {
	t.Helper()
	rows := make([]*domain.Skill, 0, len(names))
	for i, n := range names {
		rows = append(rows, &domain.Skill{
			ID:        string(rune('a' + i)),
			Name:      n,
			WhenToUse: "The user asks about " + n + ".",
			Enabled:   true,
		})
	}
	block, _ := skill.Compose(rows, nil, 0, 0)
	if block == "" {
		t.Fatal("the fixture composed an empty index")
	}
	return block
}

// **The acceptance line: `prompt_sha256` byte-identical for a company with no
// skills.** An empty builder and no builder look the same downstream until the
// prompt they compose into is hashed — and on Anthropic deployments that hash
// is what the cached prefix is keyed on, so a block that appeared as an empty
// section would move every existing tenant's cache entry and every published
// number on the day this shipped.
func TestNoSkillsLeavesThePromptByteIdentical(t *testing.T) {
	llm := &recordingLLM{reply: "ok"}
	before, err := compositionFactory()(app.AgentSpec{Primary: llm, Light: llm, Persona: persona})
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	after, err := compositionFactory()(app.AgentSpec{
		Primary: llm, Light: llm, Persona: persona, SkillIndex: "",
	})
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}

	beforeSum := sha256.Sum256([]byte(before.GetSystemPrompt()))
	afterSum := sha256.Sum256([]byte(after.GetSystemPrompt()))
	if hex.EncodeToString(beforeSum[:]) != hex.EncodeToString(afterSum[:]) {
		t.Errorf("an empty skill index changed the composed prompt:\n%s\nvs\n%s",
			hex.EncodeToString(beforeSum[:8]), hex.EncodeToString(afterSum[:8]))
	}

	// And the discriminating arm, so the assertion above is known to be
	// capable of failing: a company *with* skills must hash differently.
	with, err := compositionFactory()(app.AgentSpec{
		Primary: llm, Light: llm, Persona: persona,
		SkillIndex: skillIndexBlock(t, "Weekly sales report"),
	})
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	withSum := sha256.Sum256([]byte(with.GetSystemPrompt()))
	if hex.EncodeToString(withSum[:]) == hex.EncodeToString(beforeSum[:]) {
		t.Error("a company with a skill composed the same prompt as one without; the block never reached the prompt")
	}
}

// Facts, then procedures, then the instructions that act on both. A persona
// saying "follow our weekly reporting procedure" reads correctly only once the
// model has been shown there is one.
func TestTheSkillIndexSitsBetweenTheFactsAndThePersona(t *testing.T) {
	llm := &recordingLLM{reply: "ok"}
	index := skillIndexBlock(t, "Weekly sales report")
	agent, err := compositionFactory()(app.AgentSpec{
		Primary: llm, Light: llm,
		CompanyContext: companyBlock,
		SkillIndex:     index,
		Persona:        persona,
		SystemAddendum: reportDirective(),
	})
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}

	prompt := agent.GetSystemPrompt()
	companyAt := strings.Index(prompt, companyBlock)
	indexAt := strings.Index(prompt, skill.Header)
	personaAt := strings.Index(prompt, persona)
	directiveAt := strings.Index(prompt, "[REPORT REQUEST")
	switch {
	case indexAt < 0:
		t.Fatal("the skill index did not reach the system prompt")
	case companyAt < 0 || personaAt < 0 || directiveAt < 0:
		t.Fatalf("company at %d, persona at %d, directive at %d", companyAt, personaAt, directiveAt)
	case companyAt > indexAt:
		t.Error("the index landed before the company facts")
	case indexAt > personaAt:
		t.Error("the index landed after the persona; a persona that references a procedure must read after it")
	case personaAt > directiveAt:
		t.Error("the persona landed after the turn directive")
	}
}

// The index carries triggers, never steps. If a body ever reaches the system
// prompt the whole trade this feature makes has been reversed — thirty
// procedures would cost thirty procedures a turn, and on a cached deployment
// they would invalidate the prefix every time the model used a different one.
func TestTheSystemPromptCarriesNoSkillBody(t *testing.T) {
	llm := &recordingLLM{reply: "ok"}
	agent, err := compositionFactory()(app.AgentSpec{
		Primary: llm, Light: llm,
		SkillIndex: skillIndexBlock(t, "Weekly sales report"),
	})
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	prompt := agent.GetSystemPrompt()
	if strings.Contains(prompt, skill.FrameOpen) {
		t.Error("a framed skill body reached the system prompt")
	}
	if !strings.Contains(prompt, "load_skill") {
		t.Error("the prompt carries an index the model is never told how to open")
	}
}
