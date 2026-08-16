package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
)

// recordingConn answers every probe with one value and remembers what it was
// asked. The assertion that matters is on `queries`: a refused column must not
// reach the tenant's database at all, because a probe that runs and then
// discards its answer has already read the data.
type recordingConn struct {
	queries []string
	value   string
}

func (c *recordingConn) ExecuteReadOnly(_ context.Context, sql string, _ int) (*db.QueryResult, error) {
	c.queries = append(c.queries, sql)
	return &db.QueryResult{
		Columns: []string{"v"},
		Rows:    []map[string]interface{}{{"v": c.value}},
		Count:   1,
	}, nil
}

func (c *recordingConn) ExecuteReadOnlyParams(context.Context, string, []any, int) (*db.QueryResult, error) {
	return nil, nil
}
func (c *recordingConn) ExtractSchema(context.Context) (*db.SchemaMetadata, error) { return nil, nil }
func (c *recordingConn) Ping(context.Context) error                                { return nil }
func (c *recordingConn) Close() error                                              { return nil }

// staticSchema is the one table the probe needs to place a column in.
type staticSchema struct{ meta *db.SchemaMetadata }

func (s staticSchema) FetchSchema(context.Context, string, string, bool) (*db.SchemaMetadata, error) {
	return s.meta, nil
}

func customersSchema() *db.SchemaMetadata {
	return &db.SchemaMetadata{Tables: []db.TableInfo{{
		Name: "customers",
		Columns: []db.ColumnInfo{
			{Name: "email"}, {Name: "city"}, {Name: "keterangan"},
		},
	}}}
}

// T-H10: filter on an email column, match nothing, and receive the tenant's
// real customer emails — data the user's own query did not return, on a path
// no output guardrail sees.
func TestProbeSkipsAPIIColumnUnlessTheTenantAllowsIt(t *testing.T) {
	const sql = "SELECT * FROM customers\nWHERE email = 'budi@examle.co.id'"

	cases := []struct {
		mode       domain.PIIRedactionMode
		wantProbed bool
	}{
		{domain.PIIRedactionStrict, false},
		{"", false},                          // unset reads as strict
		{domain.PIIRedactionContactOK, true}, // the tenant asked for contact details
		{domain.PIIRedactionOff, true},
	}

	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			conn := &recordingConn{value: "sales@acme.co.id"}
			got := probeEmptyResult(context.Background(), conn, staticSchema{customersSchema()},
				"co-1", "src-1", "postgres", sql, tc.mode)

			if probed := len(got) > 0; probed != tc.wantProbed {
				t.Errorf("probe returned %d findings, want probed=%v", len(got), tc.wantProbed)
			}
			if !tc.wantProbed && len(conn.queries) != 0 {
				t.Errorf("a refused column still hit the database: %v", conn.queries)
			}
		})
	}
}

// The name says nothing and the contents say everything. This is the half a
// column-name list cannot catch, and the reason the values are classified after
// they come back.
func TestProbeDiscardsAColumnWhoseValuesArePII(t *testing.T) {
	const sql = "SELECT * FROM customers\nWHERE keterangan = 'vip'"
	conn := &recordingConn{value: "budi@example.co.id"}

	got := probeEmptyResult(context.Background(), conn, staticSchema{customersSchema()},
		"co-1", "src-1", "postgres", sql, domain.PIIRedactionStrict)

	if len(got) != 0 {
		t.Fatalf("probe disclosed %v; a column of email addresses is contact-class whatever it is called", got)
	}
	if len(conn.queries) != 1 {
		t.Errorf("expected exactly one probe query before the values were judged, got %v", conn.queries)
	}
}

// The case the probe exists for has to keep working: an ordinary label column,
// under the strictest policy, still answers the padded-value question that
// produced a fabricated figure in the first place (T-Q9).
func TestProbeStillAnswersTheOrdinaryColumnUnderStrict(t *testing.T) {
	const sql = "SELECT sum(amount) FROM customers\nWHERE city = 'Bandung'"
	conn := &recordingConn{value: "Bandung "}

	got := probeEmptyResult(context.Background(), conn, staticSchema{customersSchema()},
		"co-1", "src-1", "postgres", sql, domain.PIIRedactionStrict)

	if len(got) != 1 {
		t.Fatalf("probe returned %d findings, want 1", len(got))
	}
	if got[0]["column"] != "city" {
		t.Errorf("probed %v, want the city column", got[0]["column"])
	}
	values, _ := got[0]["actual_values"].([]string)
	if len(values) != 1 || !strings.Contains(values[0], "Bandung ") {
		t.Errorf("actual_values = %v, want the padded value quoted", values)
	}
}

// The log line must name the column, never its contents (T-H7's rule applied to
// T-H10's disclosure).
func TestProbedColumnsNamesColumnsAndNoValues(t *testing.T) {
	probes := []map[string]interface{}{
		{"table": "customers", "column": "city", "actual_values": []string{`"Bandung "`}},
		{"table": "orders", "column": "channel", "actual_values": []string{`"web"`}},
	}
	got := probedColumns(probes)
	if got != "customers.city,orders.channel" {
		t.Errorf("probedColumns = %q", got)
	}
	if strings.Contains(got, "Bandung") || strings.Contains(got, "web") {
		t.Errorf("probedColumns leaked a value: %q", got)
	}
}
