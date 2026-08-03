package agenttemplates

import (
	"slices"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/tools"
)

// The golden suite runs against the real config/agent_templates.yaml, not a
// fixture — the same rule internal/guardrails/golden_test.go follows, for the
// same reason: the file under test has to be the file that ships. A template is
// prompt text that reaches a tenant's agent, and the failure mode of a bad edit
// here is silent. The gallery renders, the card fills the form, and the agent
// is subtly wrong about its own job.
const configPath = "../../config/agent_templates.yaml"

// The six the ticket names. Pinned as a set rather than a count so that
// deleting Marketing and adding two more does not pass.
var expectedKeys = []string{
	"finance", "sales", "operations", "marketing", "people", "support",
}

func loadShipped(t *testing.T) *Set {
	t.Helper()
	// tools.AllNames, not a deployment's registry: see LoadFromFile.
	s, err := LoadFromFile(configPath, tools.AllNames())
	if err != nil {
		t.Fatalf("LoadFromFile(%s): %v", configPath, err)
	}
	return s
}

// The gate: the shipped file loads under the same validation the boot applies.
// If this fails, `cmd/api` does not start.
func TestTheShippedGalleryLoads(t *testing.T) {
	got := loadShipped(t).Keys()
	slices.Sort(got)
	want := slices.Clone(expectedKeys)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("gallery keys = %v, want %v", got, want)
	}
}

// Every card has to fill the form it opens. A template with no persona is the
// empty textarea this ticket exists to replace, and one with no starter
// questions leaves a new thread with nothing to click.
func TestEveryTemplateIsComplete(t *testing.T) {
	for _, tpl := range loadShipped(t).All() {
		t.Run(tpl.Key, func(t *testing.T) {
			if tpl.Name == "" {
				t.Error("no name")
			}
			if tpl.Description == "" {
				t.Error("no description — the card is a name and a blank line")
			}
			if len(tpl.SuggestedTools) == 0 {
				t.Error("no suggested tools — an agent that can reach nothing")
			}
			if len(tpl.SourceHints) == 0 {
				t.Error("no source hints")
			}
			if len(tpl.StarterQuestions) < 2 {
				t.Errorf("%d starter questions, want at least 2", len(tpl.StarterQuestions))
			}
			// get_schema and run_sql are how any of these answer anything. A
			// card that suggests neither produces an agent that can only talk.
			for _, needed := range []string{"get_schema", "run_sql"} {
				if !slices.Contains(tpl.SuggestedTools, needed) {
					t.Errorf("does not suggest %s", needed)
				}
			}
		})
	}
}

// A card's tool list is the allowlist its agent is created with, and the agent
// gets nothing else. That makes an omission here invisible in exactly the way a
// permission is not: nobody chose it, and the product never mentions it.
//
// The live failure this pins: the Sales card suggested get_schema, run_sql and
// create_visualization, so an agent made from it was asked for a sales overview
// "report in PDF" and had no generate_document to call. It answered with a
// markdown document in the chat and told the user to print it.
func TestEveryCardCanHandBackAFile(t *testing.T) {
	for _, tpl := range loadShipped(t).All() {
		if !slices.Contains(tpl.SuggestedTools, "generate_document") {
			t.Errorf("%s: does not suggest generate_document — an agent for this job "+
				"cannot answer a request for a report with a report", tpl.Key)
		}
	}
}

// The pair. create_visualization returns a card_id and a dashboard_cards array
// and nothing a user can open; create_dashboard is what turns those into a URL.
// A card that suggests the first without the second produces an agent whose
// charts have nowhere to go — and the system prompt tells that agent so, which
// is not the same as it being a sensible thing to ship.
func TestCardsAndDashboardsTravelTogether(t *testing.T) {
	for _, tpl := range loadShipped(t).All() {
		if slices.Contains(tpl.SuggestedTools, "create_visualization") &&
			!slices.Contains(tpl.SuggestedTools, "create_dashboard") {
			t.Errorf("%s: suggests create_visualization without create_dashboard", tpl.Key)
		}
	}
}

// The two that act rather than answer stay off every card. schedule_task books
// recurring spend on the tenant's credits and propose_action reaches outside
// Argentum — an admin ticks those deliberately or not at all, and inheriting one
// from a card picked for its persona is not deliberate.
func TestNoCardPreTicksAToolThatActs(t *testing.T) {
	for _, tpl := range loadShipped(t).All() {
		for _, acts := range []string{"schedule_task", "propose_action"} {
			if slices.Contains(tpl.SuggestedTools, acts) {
				t.Errorf("%s: pre-ticks %s, which an admin should choose knowingly", tpl.Key, acts)
			}
		}
	}
}

// The load-bearing property of the whole track: a template carries the shape of
// a *job*, and the business specifics arrive from the company profile (T-B1) at
// turn time. "the business described above" is the phrase that hands off to
// that block — a persona that stops referring to it has started describing an
// industry, which is wrong for the next tenant who picks the card and tells
// them nothing.
func TestEveryPersonaDefersToTheCompanyProfile(t *testing.T) {
	for _, tpl := range loadShipped(t).All() {
		if !strings.Contains(strings.ToLower(tpl.Persona), "described above") {
			t.Errorf("%s: the persona does not refer to the business described above:\n%s",
				tpl.Key, tpl.Persona)
		}
	}
}

// Hints are matched case-insensitively at the word start. Storing them
// lowercased keeps that one rule in one place — the matcher lowercases the
// haystack, and a capitalised hint here would be a second rule nobody wrote
// down. Two characters is the floor: "ad" already over-matches, and anything
// shorter pre-ticks every source a tenant owns.
func TestSourceHintsAreMatchable(t *testing.T) {
	for _, tpl := range loadShipped(t).All() {
		for _, h := range tpl.SourceHints {
			if h != strings.ToLower(h) {
				t.Errorf("%s: hint %q is not lowercase", tpl.Key, h)
			}
			if len(h) < 2 {
				t.Errorf("%s: hint %q is too short to mean anything", tpl.Key, h)
			}
			if strings.TrimSpace(h) != h {
				t.Errorf("%s: hint %q has surrounding whitespace", tpl.Key, h)
			}
		}
	}
}

// --- the boot failures, each one named ---------------------------------------

// A malformed gallery has to fail at boot with an error that names the offender.
// The reader is somebody who just edited one line of YAML.
func TestValidationFailures(t *testing.T) {
	valid := Template{Key: "finance", Name: "Finance", Persona: "You serve finance."}

	cases := []struct {
		name    string
		cfg     Config
		wantMsg string
	}{
		{
			name:    "no templates at all",
			cfg:     Config{},
			wantMsg: "no templates",
		},
		{
			name:    "a template with no key",
			cfg:     Config{Templates: []Template{{Name: "Finance", Persona: "x"}}},
			wantMsg: "no key",
		},
		{
			name:    "a duplicate key",
			cfg:     Config{Templates: []Template{valid, valid}},
			wantMsg: `"finance" is defined twice`,
		},
		{
			name:    "a template with no name",
			cfg:     Config{Templates: []Template{{Key: "finance", Persona: "x"}}},
			wantMsg: `"finance" has no name`,
		},
		{
			name:    "a template with no persona",
			cfg:     Config{Templates: []Template{{Key: "finance", Name: "Finance"}}},
			wantMsg: `"finance" has no persona`,
		},
		{
			name: "a tool no registry knows",
			cfg: Config{Templates: []Template{{
				Key: "finance", Name: "Finance", Persona: "x",
				SuggestedTools: []string{"run_sql", "send_email"},
			}}},
			wantMsg: `unknown tool "send_email"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.cfg, tools.AllNames())
			if err == nil {
				t.Fatal("accepted a gallery that should have failed the boot")
			}
			if !strings.Contains(err.Error(), c.wantMsg) {
				t.Errorf("err = %q, want it to contain %q", err, c.wantMsg)
			}
		})
	}
}

// Whitespace-only fields are the same failure as empty ones — a persona of
// three newlines prefills nothing.
func TestWhitespaceOnlyFieldsAreEmpty(t *testing.T) {
	_, err := New(Config{Templates: []Template{{
		Key: "finance", Name: "Finance", Persona: "   \n\t ",
	}}}, tools.AllNames())
	if err == nil || !strings.Contains(err.Error(), "no persona") {
		t.Fatalf("err = %v, want a complaint about the persona", err)
	}
}

func TestLoadFromFileMissingPath(t *testing.T) {
	// Missing is a boot failure too. A deployment that started without its
	// gallery would show every tenant an empty create screen and say so only in
	// a log line nobody reads.
	if _, err := LoadFromFile("testdata/does-not-exist.yaml", tools.AllNames()); err == nil {
		t.Fatal("LoadFromFile accepted a missing path")
	}
}

func TestLoadFromFileNamesTheFileOnAValidationFailure(t *testing.T) {
	// Validated against a registry that knows nothing, so the shipped file's
	// own tools become unknown — the cheapest way to reach the error path with
	// the real path in it.
	_, err := LoadFromFile(configPath, nil)
	if err == nil {
		t.Fatal("the shipped gallery validated against an empty registry")
	}
	if !strings.Contains(err.Error(), configPath) {
		t.Errorf("err = %q, want it to name %s", err, configPath)
	}
}

// --- per-deployment narrowing -------------------------------------------------

// generate_document exists only where object storage does (stack.go). A card
// that pre-ticks it on a deployment without one produces a form that fails on
// first save, with an error naming a tool the admin never chose.
func TestForRegistryDropsToolsThisDeploymentDoesNotRun(t *testing.T) {
	set := loadShipped(t)

	full := tools.AllNames()
	if !slices.Contains(full, "generate_document") {
		t.Fatal("generate_document is no longer in the release registry; this test needs a new example")
	}
	without := slices.DeleteFunc(slices.Clone(full), func(n string) bool { return n == "generate_document" })

	var suggestedItAtAll bool
	for _, tpl := range set.All() {
		if slices.Contains(tpl.SuggestedTools, "generate_document") {
			suggestedItAtAll = true
		}
	}
	if !suggestedItAtAll {
		t.Fatal("no shipped template suggests generate_document; this test proves nothing")
	}

	for _, tpl := range set.ForRegistry(without) {
		if slices.Contains(tpl.SuggestedTools, "generate_document") {
			t.Errorf("%s still suggests generate_document on a deployment without object storage", tpl.Key)
		}
		// The rest of the card is untouched: dropping one checkbox must not
		// drop the persona that made the card worth picking.
		if tpl.Persona == "" || len(tpl.StarterQuestions) == 0 {
			t.Errorf("%s lost more than the tool", tpl.Key)
		}
	}

	// And the Set itself is unchanged — narrowing returns copies, so an API
	// process that answered one request with a reduced registry does not serve
	// the reduced gallery to the next.
	for _, tpl := range set.All() {
		if tpl.Key == "finance" && !slices.Contains(tpl.SuggestedTools, "generate_document") {
			t.Error("ForRegistry mutated the loaded gallery")
		}
	}
}

// A nil Set is what a wiring with no gallery holds. Every accessor has to
// answer rather than panic: the create form then offers the blank path only,
// which is the product exactly as it was before this ticket.
func TestNilSetIsTheBlankPathOnly(t *testing.T) {
	var s *Set
	if got := s.All(); len(got) != 0 {
		t.Errorf("All() = %v, want empty", got)
	}
	if got := s.Keys(); len(got) != 0 {
		t.Errorf("Keys() = %v, want empty", got)
	}
	if s.Has("finance") {
		t.Error("Has() answered true with no gallery loaded")
	}
	if got := s.ForRegistry([]string{"run_sql"}); len(got) != 0 {
		t.Errorf("ForRegistry() = %v, want empty", got)
	}
}
