package eval

import (
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/llmusage"
)

func reportWith(model string, served []ServedModel, declared []string) Report {
	rep := Summarize("demo-retail-v1", model, time.Now().Add(-time.Minute), []Result{
		{ID: "a", Category: "simple_aggregate", Passed: true},
		{ID: "b", Category: "simple_aggregate", Passed: false, Failures: []string{"wrong"}},
	})
	rep.Served = served
	rep.Declared = declared
	return rep
}

func TestReportNamesWhatServedIt(t *testing.T) {
	rep := reportWith("deepseek/deepseek-v3.2",
		[]ServedModel{{Model: "deepseek/deepseek-v3.2", Provider: "DeepInfra", Responses: 56}}, nil)

	got := rep.Text()
	if !strings.Contains(got, "served:     deepseek/deepseek-v3.2 via DeepInfra (56 responses)") {
		t.Fatalf("summary does not name what served the run:\n%s", got)
	}
}

// The absence has to be legible too. Every number published before this ticket
// is in exactly this state, and a blank line would read as "same as the model
// line" rather than as "nobody recorded it".
func TestReportSaysSoWhenNothingWasReported(t *testing.T) {
	got := reportWith("moonshotai/kimi-k2.6", nil, nil).Text()
	if !strings.Contains(got, "served:     not reported by the provider") {
		t.Fatalf("an unreported identity is invisible:\n%s", got)
	}
	if !strings.Contains(got, "names no revision") {
		t.Fatalf("the consequence of the absence is not stated:\n%s", got)
	}
}

func TestReportFlagsARunAnsweredByTwoIdentities(t *testing.T) {
	got := reportWith("deepseek/deepseek-v3.2", []ServedModel{
		{Model: "deepseek/deepseek-v3.2", Provider: "DeepInfra", Responses: 40},
		{Model: "deepseek/deepseek-v3.2", Provider: "Fireworks", Responses: 16},
	}, nil).Text()

	if !strings.Contains(got, "more than one identity answered this run") {
		t.Fatalf("a mid-set re-route is not flagged:\n%s", got)
	}
	if !strings.Contains(got, "DeepInfra") || !strings.Contains(got, "Fireworks") {
		t.Fatalf("both routes must be named:\n%s", got)
	}
}

func TestReportFlagsAModelTheSetDoesNotDeclare(t *testing.T) {
	declared := []string{"moonshotai/kimi-k2.6", "deepseek/deepseek-v3.2"}

	stray := reportWith("openai/gpt-5-nano", nil, declared).Text()
	if !strings.Contains(stray, "is not one of the models this set declares") {
		t.Fatalf("an undeclared model scored the set silently:\n%s", stray)
	}

	// And the declared case must stay quiet, or the warning is noise nobody
	// reads by the third run.
	ok := reportWith("moonshotai/kimi-k2.6", nil, declared).Text()
	if strings.Contains(ok, "does not declare") {
		t.Fatalf("a declared model was warned about:\n%s", ok)
	}
}

func TestServedFromCarriesCountsAndOrder(t *testing.T) {
	got := ServedFrom([]llmusage.ObservedServing{
		{Serving: llmusage.Serving{Model: "a", Provider: "P"}, Responses: 3},
		{Serving: llmusage.Serving{Model: "b"}, Responses: 1},
	})
	if len(got) != 2 || got[0].Model != "a" || got[0].Provider != "P" || got[0].Responses != 3 {
		t.Fatalf("ServedFrom = %+v", got)
	}
	if ServedFrom(nil) != nil {
		t.Fatalf("no observation must convert to nil, not to an empty row")
	}
}

func TestSameServingComparesIdentityNotVolume(t *testing.T) {
	a := []ServedModel{{Model: "m", Provider: "P", Responses: 56}}
	// Same route, different number of responses: still the same measurement.
	b := []ServedModel{{Model: "m", Provider: "P", Responses: 8}}
	if !SameServing(a, b) {
		t.Fatalf("response count must not make two runs incomparable")
	}
	c := []ServedModel{{Model: "m", Provider: "Q", Responses: 56}}
	if SameServing(a, c) {
		t.Fatalf("a provider swap under the same model id must compare as different")
	}
	if SameServing(a, nil) {
		t.Fatalf("a known identity and an unknown one are not the same")
	}
	if !SameServing(nil, nil) {
		t.Fatalf("two unreported runs are equally unreadable, which compares as equal")
	}
}

func TestSetDeclaresIsPermissiveWhenTheSetNamesNothing(t *testing.T) {
	var s Set
	if !s.Declares("anything/at-all") {
		t.Fatalf("a set with no models block must not warn about every model")
	}
	s.Models = []string{"moonshotai/kimi-k2.6"}
	if !s.Declares("  MoonshotAI/Kimi-K2.6 ") {
		t.Fatalf("declaration must ignore case and surrounding space")
	}
	if s.Declares("openai/gpt-5-nano") {
		t.Fatalf("an undeclared model must not read as declared")
	}
}
