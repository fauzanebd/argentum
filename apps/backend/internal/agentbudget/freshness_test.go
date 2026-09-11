package agentbudget

import "testing"

func freshResult(verdict, note string) string {
	return `{"row_count":1,"rows":[{"total":1}],"data_freshness":{"verdict":"` + verdict + `","note":"` + note + `"}}`
}

// The rule the ticket turns on: a turn that read a fresh source and a stale one
// has a stale answer in it, whichever order the model called them in.
func TestTheTurnCarriesTheWorstVerdictNotTheLast(t *testing.T) {
	cases := []struct {
		name    string
		results []string
		want    string
	}{
		{"stale then fresh", []string{freshResult("stale", "3 days"), freshResult("fresh", "")}, "stale"},
		{"fresh then stale", []string{freshResult("fresh", ""), freshResult("stale", "3 days")}, "stale"},
		{"warn then fresh", []string{freshResult("warn", "6 hours"), freshResult("fresh", "")}, "warn"},
		{"stale then warn", []string{freshResult("stale", "3 days"), freshResult("warn", "6 hours")}, "stale"},
		{"only fresh", []string{freshResult("fresh", "")}, "fresh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := New(Default())
			for _, r := range tc.results {
				tr.Observe("run_sql", r, nil)
			}
			if got := tr.Snapshot().Freshness; got != tc.want {
				t.Errorf("freshness = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestTheNoteTravelsWithTheVerdictItBelongsTo(t *testing.T) {
	tr := New(Default())
	tr.Observe("run_sql", freshResult("warn", "6 hours"), nil)
	tr.Observe("query_metric", freshResult("stale", "3 days"), nil)
	snap := tr.Snapshot()
	if snap.Freshness != "stale" {
		t.Fatalf("freshness = %q; want stale", snap.Freshness)
	}
	if snap.FreshnessNote != "3 days" {
		t.Errorf("note = %q; want the stale verdict's note, not the warn one's", snap.FreshnessNote)
	}
}

// Every turn on a deployment where nobody configured freshness.
func TestAResultWithNoFreshnessBlockReportsNothing(t *testing.T) {
	tr := New(Default())
	tr.Observe("run_sql", `{"row_count":3,"rows":[{"a":1}]}`, nil)
	tr.Observe("get_schema", `{"tables":[]}`, nil)
	if got := tr.Snapshot().Freshness; got != "" {
		t.Errorf("freshness = %q; want empty on a turn nothing reported one for", got)
	}
}

// A query that matched nothing against a source that has not loaded in three
// days is exactly the case where the currency is the answer, so the verdict is
// read independently of the row count.
func TestAnEmptyResultStillCarriesItsFreshness(t *testing.T) {
	tr := New(Default())
	tr.Observe("run_sql", `{"row_count":0,"rows":[],"data_freshness":{"verdict":"stale","note":"3 days"}}`, nil)
	snap := tr.Snapshot()
	if snap.Freshness != "stale" {
		t.Errorf("freshness = %q; want stale", snap.Freshness)
	}
	if snap.EmptyResults != 1 {
		t.Errorf("empty_results = %d; want 1 — reading freshness must not consume the row count", snap.EmptyResults)
	}
}

func TestAFailedCallContributesNoVerdict(t *testing.T) {
	tr := New(Default())
	tr.Observe("run_sql", freshResult("stale", "3 days"), errTest{})
	if got := tr.Snapshot().Freshness; got != "" {
		t.Errorf("freshness = %q; a call that errored says nothing about the source's currency", got)
	}
}

type errTest struct{}

func (errTest) Error() string { return "boom" }
