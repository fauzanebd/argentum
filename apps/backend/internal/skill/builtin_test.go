package skill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// The golden test over the real files, the way config/guardrails.yaml and
// config/agent_templates.yaml both have one. A shipped skill that breaks a cap
// must fail this suite rather than a tenant's turn.
func TestShippedSkillsLoadAndPassTheTenantRules(t *testing.T) {
	skills, err := LoadBuiltins("../../config/skills")
	if err != nil {
		t.Fatalf("the shipped skills do not load: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("loaded %d shipped skills, want 2 — the set is deliberately small, so a third is a decision and not a drive-by", len(skills))
	}
	for _, s := range skills {
		if !s.IsBuiltin() {
			t.Errorf("%q has source %q, want a builtin: prefix", s.Name, s.Source)
		}
		if !s.Enabled {
			t.Errorf("%q loaded disabled; a shipped skill nobody can use is a shipped index line", s.Name)
		}
		if err := s.Validate(); err != nil {
			t.Errorf("%q fails the rules the API enforces on tenants: %v", s.Name, err)
		}
	}
}

// The index line is the part that rides *every* turn, so the shipped set's
// contribution to it is worth pinning rather than discovering in a token bill.
func TestShippedSkillsCostALineEach(t *testing.T) {
	skills, err := LoadBuiltins("../../config/skills")
	if err != nil {
		t.Fatal(err)
	}
	index, dropped := Compose(skills, nil, DefaultIndexMaxLines, DefaultIndexMaxChars)
	if len(dropped) != 0 {
		t.Errorf("the shipped set does not fit its own default index bound: dropped %v", dropped)
	}
	// Two lines and a header. If this number moves, the always-on cost of
	// shipping skills moved with it.
	if n := strings.Count(index, "\n- "); n != 2 {
		t.Errorf("index carries %d shipped lines, want 2", n)
	}
	// Measured 701 characters on 2026-08-25 — header plus two lines. The bound
	// is the measurement with a little headroom, not a guess: what it is here
	// to catch is a third shipped skill or a `when_to_use` that grew into a
	// paragraph, both of which are always-on cost paid by every tenant on
	// every turn.
	if len(index) > 900 {
		t.Errorf("the shipped index block is %d chars, against 701 measured when it was written; it rides every turn", len(index))
	}
}

// A skill must not restate a guideline — the rule the shipped set is two files
// because of. This cannot be checked mechanically in general, so it checks the
// one restatement that was actually drafted and cut: the zero-row rule.
func TestNoShippedSkillRestatesTheZeroRowGuideline(t *testing.T) {
	skills, err := LoadBuiltins("../../config/skills")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		body := strings.ToLower(s.Body)
		// "zero rows" as the *subject* of a step is the restatement. The
		// period-over-period skill mentions the rule while applying it to a
		// denominator, which is the distinction the whole ticket turns on, so
		// the assertion is on the phrasing a restatement would use.
		for _, banned := range []string{
			"if a query returns zero rows",
			"that is not zero and it is not a number",
		} {
			if strings.Contains(body, banned) {
				t.Errorf("%q restates the no-fabrication guideline (%q); a skill is for what the model should do on *some* turns", s.Name, banned)
			}
		}
	}
}

func TestFrontMatterParsing(t *testing.T) {
	t.Run("a wrapped when_to_use is joined", func(t *testing.T) {
		s, err := parseBuiltin("k", "---\nname: A\nwhen_to_use: first\n  second\n---\nBody.\n")
		if err != nil {
			t.Fatal(err)
		}
		if s.WhenToUse != "first second" {
			t.Errorf("when_to_use = %q, want the continuation joined", s.WhenToUse)
		}
	})

	t.Run("the body keeps its own --- rules", func(t *testing.T) {
		s, err := parseBuiltin("k", "---\nname: A\nwhen_to_use: w\n---\nAbove.\n\n---\n\nBelow.\n")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(s.Body, "---") || !strings.Contains(s.Body, "Below.") {
			t.Errorf("body = %q, want the horizontal rule and everything after it", s.Body)
		}
	})

	t.Run("the source names the file", func(t *testing.T) {
		s, err := parseBuiltin("period-over-period", "---\nname: A\nwhen_to_use: w\n---\nBody.\n")
		if err != nil {
			t.Fatal(err)
		}
		if s.Source != domain.SkillSourceBuiltinPrefix+"period-over-period" {
			t.Errorf("source = %q", s.Source)
		}
	})

	for _, tc := range []struct{ name, in string }{
		{"no front matter", "name: A\nBody"},
		{"unclosed front matter", "---\nname: A\nBody"},
		{"unknown key", "---\nname: A\nwhen_to_use: w\ncolour: blue\n---\nBody"},
		{"not key: value", "---\nname: A\nnonsense\n---\nBody"},
	} {
		t.Run(tc.name+" is refused", func(t *testing.T) {
			if _, err := parseBuiltin("k", tc.in); err == nil {
				t.Error("parsed without error")
			}
		})
	}
}

// A malformed shipped skill fails the boot, not the request.
func TestLoadRefusesAFileThatBreaksACap(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("N", domain.MaxSkillNameChars+1)
	must(t, os.WriteFile(filepath.Join(dir, "bad.md"),
		[]byte("---\nname: "+long+"\nwhen_to_use: w\n---\nBody.\n"), 0o600))

	if _, err := LoadBuiltins(dir); err == nil {
		t.Fatal("a shipped skill over the name cap loaded; every deployment should refuse to boot on it")
	}
}

// Two files claiming one name is a load_skill call with no correct answer.
func TestLoadRefusesDuplicateNames(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("---\nname: Same\nwhen_to_use: w\n---\nA.\n"), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte("---\nname: same\nwhen_to_use: w\n---\nB.\n"), 0o600))

	if _, err := LoadBuiltins(dir); err == nil {
		t.Fatal("two shipped skills with one name loaded")
	}
}

// A deployment may ship none, and an absent directory is not a failure.
func TestAnAbsentDirectoryIsNotAnError(t *testing.T) {
	skills, err := LoadBuiltins(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("absent directory: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("loaded %d skills from nowhere", len(skills))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// --- the decorator ---

type stubSkills struct {
	rows []*domain.Skill
	err  error
}

func (s *stubSkills) Create(context.Context, *domain.Skill) error { return nil }
func (s *stubSkills) GetByID(context.Context, string, string) (*domain.Skill, error) {
	return nil, domain.ErrNotFound
}
func (s *stubSkills) GetByName(_ context.Context, _, name string) (*domain.Skill, error) {
	for _, r := range s.rows {
		if strings.EqualFold(r.Name, name) {
			return r, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (s *stubSkills) ListByCompany(context.Context, string) ([]*domain.Skill, error) {
	return s.rows, nil
}
func (s *stubSkills) ListEnabledForIndex(context.Context, string) ([]*domain.Skill, error) {
	return s.rows, s.err
}
func (s *stubSkills) Update(context.Context, *domain.Skill) error                     { return nil }
func (s *stubSkills) Delete(context.Context, string, string) error                    { return nil }
func (s *stubSkills) CountByCompany(context.Context, string) (int, error)             { return len(s.rows), nil }
func (s *stubSkills) SetAgentBinding(context.Context, string, string, []string) error { return nil }
func (s *stubSkills) AgentBinding(context.Context, string, string) ([]string, error) {
	return nil, nil
}

func builtinFixture() []*domain.Skill {
	return []*domain.Skill{{
		Name: "Period-over-period comparison", WhenToUse: "w", Body: "b",
		Enabled: true, Source: domain.SkillSourceBuiltinPrefix + "period-over-period",
	}}
}

// Tenant first, ours after: when an index is truncated the tenant loses our
// procedures before they lose their own.
func TestBuiltinsSortBelowTheTenantsOwn(t *testing.T) {
	repo := WithBuiltins(&stubSkills{rows: []*domain.Skill{
		{ID: "t1", Name: "How we close the month", Enabled: true, Source: domain.SkillSourceTenant},
	}}, builtinFixture())

	got, err := repo.ListEnabledForIndex(context.Background(), "co-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d skills, want the tenant's and the shipped one", len(got))
	}
	if got[0].Name != "How we close the month" {
		t.Errorf("first is %q, want the tenant's own", got[0].Name)
	}
	if !got[1].IsBuiltin() {
		t.Errorf("second is %q, want the shipped one", got[1].Name)
	}
	if got[1].CompanyID != "co-1" {
		t.Errorf("the shipped copy carries company %q, want the asking tenant's", got[1].CompanyID)
	}
	if got[1].ID != got[1].Source {
		t.Errorf("id = %q, want the source string so binding checks can compare it", got[1].ID)
	}
}

// The shared set must never hand one tenant's company id to another's turn.
func TestBuiltinsAreCopiedPerCompany(t *testing.T) {
	builtins := builtinFixture()
	repo := WithBuiltins(&stubSkills{}, builtins)

	a, _ := repo.ListEnabledForIndex(context.Background(), "co-a")
	b, _ := repo.ListEnabledForIndex(context.Background(), "co-b")

	if a[0].CompanyID != "co-a" || b[0].CompanyID != "co-b" {
		t.Errorf("company ids leaked between tenants: %q and %q", a[0].CompanyID, b[0].CompanyID)
	}
	if builtins[0].CompanyID != "" {
		t.Errorf("the shared set was mutated: %q", builtins[0].CompanyID)
	}
}

// A tenant who writes a procedure with a shipped name meant theirs.
func TestATenantSkillShadowsABuiltinOfTheSameName(t *testing.T) {
	own := &domain.Skill{
		ID: "t1", Name: "period-over-period COMPARISON", Body: "ours",
		Enabled: true, Source: domain.SkillSourceTenant,
	}
	repo := WithBuiltins(&stubSkills{rows: []*domain.Skill{own}}, builtinFixture())

	got, err := repo.ListEnabledForIndex(context.Background(), "co-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("got %d skills, want only the tenant's — a shadowed built-in must not appear twice", len(got))
	}

	byName, err := repo.GetByName(context.Background(), "co-1", "Period-over-period comparison")
	if err != nil {
		t.Fatal(err)
	}
	if byName.Body != "ours" {
		t.Errorf("GetByName returned the shipped body; the tenant's must win")
	}
}

// The index and load_skill must agree, or an agent reads a line it cannot open.
func TestABuiltinIsOpenableByName(t *testing.T) {
	repo := WithBuiltins(&stubSkills{}, builtinFixture())

	got, err := repo.GetByName(context.Background(), "co-1", "period-over-period comparison")
	if err != nil {
		t.Fatalf("a shipped skill in the index could not be opened: %v", err)
	}
	if got.CompanyID != "co-1" || !got.IsBuiltin() {
		t.Errorf("got %+v", got)
	}

	if _, err := repo.GetByName(context.Background(), "co-1", "no such procedure"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// A repository failure is the repository's, not something to paper over with
// shipped skills that would make an index look healthy during an outage.
func TestARepositoryErrorIsNotMaskedByBuiltins(t *testing.T) {
	repo := WithBuiltins(&stubSkills{err: errors.New("control DB down")}, builtinFixture())
	if _, err := repo.ListEnabledForIndex(context.Background(), "co-1"); err == nil {
		t.Fatal("a repository error was swallowed")
	}
}
