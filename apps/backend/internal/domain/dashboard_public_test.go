package domain

import (
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/dashboard/spec"
)

// The 2026-09-03 gate's finding: `/share/dashboard/:token` was serving the
// panel SQL to a caller with no session.
func TestPublicCopyWithholdsEverythingAVisitorMustNotSee(t *testing.T) {
	thread, creator := "thread-1", "user-1"
	d := &Dashboard{
		ID:        "dash-1",
		CompanyID: "company-1",
		SourceID:  "source-1",
		ThreadID:  &thread,
		CreatedBy: &creator,
		Title:     "Revenue",
		Spec: spec.Dashboard{
			SourceID: "source-1",
			Panels: []spec.Panel{
				{ID: "panel-1", Title: "Customers", Viz: "table", SQL: "SELECT email FROM dim_customers WHERE segment = 'vip'"},
				{ID: "panel-2", Title: "Revenue", Viz: "kpi", MetricKey: "net_revenue"},
			},
		},
	}

	pub := d.PublicCopy()

	for _, p := range pub.Spec.Panels {
		if p.SQL != "" {
			t.Errorf("panel %s still carries SQL on the public copy: %q", p.ID, p.SQL)
		}
		if p.MetricKey != "" {
			t.Errorf("panel %s still carries a metric key: %q", p.ID, p.MetricKey)
		}
	}
	for name, got := range map[string]string{
		"company_id":     pub.CompanyID,
		"source_id":      pub.SourceID,
		"spec.source_id": pub.Spec.SourceID,
	} {
		if got != "" {
			t.Errorf("%s is exposed on the public copy: %q", name, got)
		}
	}
	if pub.ThreadID != nil || pub.CreatedBy != nil {
		t.Error("the originating thread or author is exposed on the public copy")
	}

	// What the page draws must survive, or the redaction has broken the feature
	// rather than secured it.
	if pub.Title != "Revenue" || len(pub.Spec.Panels) != 2 {
		t.Fatalf("the public copy lost what the renderer needs: %+v", pub)
	}
	if pub.Spec.Panels[0].Title != "Customers" || pub.Spec.Panels[0].Viz != "table" {
		t.Error("panel title or viz did not survive redaction")
	}
}

// A copy, not a mutation: the argument is a pointer into the repository's
// result and the caller may still be using it. Getting this wrong would blank
// the SQL for the *authenticated* dashboard route too, which is the kind of
// fix that reads as working until somebody opens their own dashboard.
func TestPublicCopyDoesNotMutateTheOriginal(t *testing.T) {
	const query = "SELECT 1 FROM t"
	d := &Dashboard{
		CompanyID: "company-1",
		Spec:      spec.Dashboard{Panels: []spec.Panel{{ID: "p1", SQL: query}}},
	}
	_ = d.PublicCopy()

	if d.Spec.Panels[0].SQL != query {
		t.Errorf("PublicCopy blanked the caller's own panel SQL: %q", d.Spec.Panels[0].SQL)
	}
	if d.CompanyID != "company-1" {
		t.Error("PublicCopy blanked the caller's company id")
	}
}

// The whole payload, as a string. The two tests above name fields; this one
// catches a field added later that nobody thought to redact.
func TestNoWarehouseVocabularyLeaksIntoThePublicCopy(t *testing.T) {
	d := &Dashboard{
		CompanyID: "company-1",
		Spec: spec.Dashboard{Panels: []spec.Panel{
			{ID: "p1", Title: "Fine", SQL: "SELECT secret_column FROM secret_table"},
		}},
	}
	blob := renderForTest(d.PublicCopy())
	for _, needle := range []string{"secret_column", "secret_table", "company-1"} {
		if strings.Contains(blob, needle) {
			t.Errorf("%q reached the public copy: %s", needle, blob)
		}
	}
}
