package domain

import (
	"errors"
	"strings"
	"testing"
)

// The caps are the ticket. Each is tested **at the boundary and one under it**,
// because an off-by-one in the permissive direction is a prompt that grows past
// the number this feature exists to be able to state, and an off-by-one in the
// strict direction is a save a tenant cannot explain.

func validSkill() *Skill {
	return &Skill{
		Name:      "Weekly sales report",
		WhenToUse: "The user asks for a weekly or regular sales summary.",
		Body:      "1. Query fact_sales for the last 7 days.\n2. Exclude cancelled orders.",
		Enabled:   true,
	}
}

func TestSkillCapsRefuseAtTheBoundaryAndPassOneUnder(t *testing.T) {
	cases := []struct {
		field string
		limit int
		set   func(s *Skill, v string)
	}{
		{"name", MaxSkillNameChars, func(s *Skill, v string) { s.Name = v }},
		{"when_to_use", MaxSkillWhenToUseChars, func(s *Skill, v string) { s.WhenToUse = v }},
		{"body", MaxSkillBodyChars, func(s *Skill, v string) { s.Body = v }},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			at := validSkill()
			tc.set(at, strings.Repeat("a", tc.limit))
			if err := at.Validate(); err != nil {
				t.Errorf("exactly %d characters was refused: %v", tc.limit, err)
			}

			over := validSkill()
			tc.set(over, strings.Repeat("a", tc.limit+1))
			err := over.Validate()
			if err == nil {
				t.Fatalf("%d characters was accepted; the limit is %d", tc.limit+1, tc.limit)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("refusal is not ErrInvalidInput: %v", err)
			}
			// The message has to name the field and the limit, or a tenant
			// staring at a form cannot act on it.
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("refusal does not name the field: %v", err)
			}
		})
	}
}

// A cap that truncates is the failure this feature cannot have: the last step
// of a procedure is where "and exclude cancelled orders" lives.
func TestSkillValidateNeverTruncates(t *testing.T) {
	s := validSkill()
	body := strings.Repeat("x", MaxSkillBodyChars+50)
	s.Body = body
	if err := s.Validate(); err == nil {
		t.Fatal("an over-long body was accepted")
	}
	if len(s.Body) != len(body) {
		t.Errorf("Validate modified the body: %d chars in, %d out — a shortened procedure is one whose last step vanished",
			len(body), len(s.Body))
	}
}

// Counted in runes, not bytes. The caps bound a prompt, and a rule that let an
// English procedure be 8,000 characters and an Indonesian one 5,300 would be a
// cap on the alphabet rather than on the cost.
func TestSkillCapsCountRunesNotBytes(t *testing.T) {
	s := validSkill()
	// Three bytes each in UTF-8, and exactly at the limit in characters.
	s.Name = strings.Repeat("あ", MaxSkillNameChars)
	if err := s.Validate(); err != nil {
		t.Errorf("%d multi-byte characters refused at a %d-character limit: %v", MaxSkillNameChars, MaxSkillNameChars, err)
	}
}

func TestSkillValidateRefusesEmptyFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(s *Skill)
	}{
		{"name", func(s *Skill) { s.Name = "   " }},
		{"when_to_use", func(s *Skill) { s.WhenToUse = "" }},
		{"body", func(s *Skill) { s.Body = "\n\t " }},
	} {
		s := validSkill()
		tc.mut(s)
		if err := s.Validate(); err == nil {
			t.Errorf("an empty %s was accepted", tc.name)
		}
	}
}

// A tenant must not be able to relabel their own text as shipped-with-the-
// product. The distinction is not cosmetic: `builtin:` carries the argument
// that a commit somebody reviewed is what makes the body trustworthy.
func TestSkillValidateRefusesAnUnknownSource(t *testing.T) {
	s := validSkill()
	s.Source = "vendor"
	if err := s.Validate(); err == nil {
		t.Error("an arbitrary source was accepted")
	}

	s = validSkill()
	s.Source = ""
	if err := s.Validate(); err != nil {
		t.Fatalf("an empty source was refused rather than defaulted: %v", err)
	}
	if s.Source != SkillSourceTenant {
		t.Errorf("source = %q, want the tenant default", s.Source)
	}

	s = validSkill()
	s.Source = SkillSourceBuiltinPrefix + "weekly-report"
	if err := s.Validate(); err != nil {
		t.Errorf("a builtin source was refused: %v", err)
	}
	if !s.IsBuiltin() {
		t.Error("IsBuiltin is false for a builtin: source")
	}
}

// The index line is what the two prompt-facing caps actually bound, so its
// worst case is arithmetic somebody can check rather than a claim.
func TestSkillIndexLineWorstCaseIsTheStatedNumber(t *testing.T) {
	s := &Skill{
		Name:      strings.Repeat("n", MaxSkillNameChars),
		WhenToUse: strings.Repeat("w", MaxSkillWhenToUseChars),
	}
	line := s.IndexLine()
	// "- " + name + " — " + when_to_use. The em dash is three bytes and one
	// character; the roadmap's 263 is characters.
	want := 2 + MaxSkillNameChars + 3 + MaxSkillWhenToUseChars
	if got := len([]rune(line)); got != want {
		t.Errorf("worst-case index line = %d characters, want %d — §3's arithmetic and this code disagree", got, want)
	}
}

// The empty binding is the rule two adjacent tables read in opposite
// directions, so it gets its own test rather than a comment.
func TestAgentAllowsSkillEmptyMeansEverything(t *testing.T) {
	a := &Agent{}
	if !a.AllowsSkill("any-skill") {
		t.Error("an agent with no binding was offered nothing; empty means every enabled company skill")
	}

	a = &Agent{SkillIDs: []string{"skill-1"}}
	if !a.AllowsSkill("skill-1") {
		t.Error("a bound skill was refused")
	}
	if a.AllowsSkill("skill-2") {
		t.Error("an unbound skill was offered to a restricted agent")
	}

	// The contrast that makes the asymmetry visible: the same empty list on the
	// MCP binding means none.
	if (&Agent{}).AllowsMCPServer("server-1") {
		t.Error("the MCP binding changed meaning; skills and MCP servers must keep reading empty in opposite directions")
	}
}
