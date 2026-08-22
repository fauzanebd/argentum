package skill

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fauzanebd/argentum/internal/domain"
)

func skills(n int) []*domain.Skill {
	out := make([]*domain.Skill, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, &domain.Skill{
			ID:        string(rune('a'+i%26)) + "-id",
			Name:      "Procedure " + string(rune('A'+i%26)),
			WhenToUse: "The user asks about topic " + string(rune('A'+i%26)) + ".",
			Enabled:   true,
		})
	}
	return out
}

// **The arm a unit test is least likely to catch, and the one that decides
// whether this feature can ship to existing tenants at all.** The block must be
// *absent* for a company with no skills, not empty: an empty
// `strings.Builder` and no builder at all look identical right up until the
// prompt they compose into is hashed, and that hash is what every published
// number for this project is keyed on.
func TestComposeReturnsNothingWhenThereIsNothingToSay(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []*domain.Skill
	}{
		{"no skills at all", nil},
		{"an empty slice", []*domain.Skill{}},
		{"every skill disabled", []*domain.Skill{{ID: "a", Name: "Off", WhenToUse: "Never.", Enabled: false}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block, dropped := Compose(tc.in, nil, 0, 0)
			if block != "" {
				t.Errorf("composed a block for %s:\n%q", tc.name, block)
			}
			if len(dropped) != 0 {
				t.Errorf("reported %d dropped skills where there was nothing to drop", len(dropped))
			}
		})
	}
}

// A skill this agent is not bound to is not in its index — and it is not
// reported as dropped either, because the tenant did not lose it to a bound.
func TestComposeIsFilteredByTheAgentBinding(t *testing.T) {
	all := []*domain.Skill{
		{ID: "s1", Name: "Bound", WhenToUse: "Sometimes.", Enabled: true},
		{ID: "s2", Name: "Unbound", WhenToUse: "Other times.", Enabled: true},
	}
	agent := &domain.Agent{SkillIDs: []string{"s1"}}

	block, dropped := Compose(all, agent.AllowsSkill, 0, 0)
	if !strings.Contains(block, "Bound") {
		t.Error("the bound skill is missing from the index")
	}
	if strings.Contains(block, "Unbound") {
		t.Errorf("an unbound skill reached this agent's prompt:\n%s", block)
	}
	if len(dropped) != 0 {
		t.Errorf("an unbound skill was reported as dropped: %v — the tenant lost nothing to a bound", dropped)
	}

	// The empty binding is every enabled skill, which is the rule this whole
	// track reads the other way from the MCP binding next door.
	block, _ = Compose(all, (&domain.Agent{}).AllowsSkill, 0, 0)
	if !strings.Contains(block, "Bound") || !strings.Contains(block, "Unbound") {
		t.Errorf("an unbound agent was not offered everything:\n%s", block)
	}
}

func TestComposeStopsAtTheLineBound(t *testing.T) {
	block, dropped := Compose(skills(25), nil, 20, 100000)
	if n := strings.Count(block, "\n- "); n != 20 {
		t.Errorf("index carries %d lines, want 20", n)
	}
	if len(dropped) != 5 {
		t.Errorf("dropped %d, want 5", len(dropped))
	}
}

// **The arm the line bound alone would have passed**, and the reason the
// character bound exists: twenty lines is not a size. Sixteen skills at T-K1's
// caps satisfy `SKILL_INDEX_MAX` and breach the character bound — a case that
// did not exist at the 6,000 this ticket originally specified, because the
// whole 20-line index tops out at 5,662.
func TestComposeStopsAtTheCharacterBoundEvenWithinTheLineBound(t *testing.T) {
	// Sixteen skills at T-K1's caps: inside the 20-line bound, and 4,598
	// characters against the 4,000 the package defaults to.
	long := make([]*domain.Skill, 0, 16)
	for i := 0; i < 16; i++ {
		long = append(long, &domain.Skill{
			ID:        string(rune('a' + i)),
			Name:      strings.Repeat("n", domain.MaxSkillNameChars-2) + string(rune('a'+i)) + "z",
			WhenToUse: strings.Repeat("w", domain.MaxSkillWhenToUseChars),
			Enabled:   true,
		})
	}

	block, dropped := Compose(long, nil, DefaultIndexMaxLines, DefaultIndexMaxChars)
	if n := strings.Count(block, "\n- "); n >= 16 {
		t.Errorf("all %d lines fit; the character bound did not bind", n)
	}
	if len(dropped) == 0 {
		t.Fatal("nothing was reported dropped, so a tenant over the character bound would never find out")
	}
	if got := utf8.RuneCountInString(block); got > DefaultIndexMaxChars {
		t.Errorf("index is %d characters, over the %d bound", got, DefaultIndexMaxChars)
	}
	// And the line count is inside the *other* bound, which is what makes this
	// case the one a line-only check would have waved through.
	if n := strings.Count(block, "\n- "); n > DefaultIndexMaxLines {
		t.Errorf("lines = %d, over the line bound too — this case no longer isolates the character bound", n)
	}
}

// Truncation is by the caller's order, which the repository fixes as
// lower(name). A tenant over a bound must lose the same skills every turn: an
// order that moved would change the cached prefix from turn to turn.
func TestComposeTruncatesDeterministically(t *testing.T) {
	first, firstDropped := Compose(skills(25), nil, 5, 100000)
	second, secondDropped := Compose(skills(25), nil, 5, 100000)
	if first != second {
		t.Error("two composes of the same input differ")
	}
	if strings.Join(firstDropped, ",") != strings.Join(secondDropped, ",") {
		t.Error("two composes dropped different skills")
	}
}

// The header has to name the tool, or the index is a list of procedures the
// agent can see and cannot open — worse than the feature's absence, because it
// reads as a broken model rather than a missing feature.
func TestIndexHeaderNamesTheTool(t *testing.T) {
	block, _ := Compose(skills(1), nil, 0, 0)
	if !strings.Contains(block, "load_skill") {
		t.Errorf("the index does not name the tool that opens it:\n%s", block)
	}
	if !strings.Contains(block, "Procedure A — The user asks about topic A.") {
		t.Errorf("the index line is not `name — when_to_use`:\n%s", block)
	}
	// No bodies. The whole trade is that they do not travel.
	if strings.Contains(block, FrameOpen) {
		t.Errorf("a framed body reached the index:\n%s", block)
	}
}
