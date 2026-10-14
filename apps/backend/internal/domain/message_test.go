package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

// RIZ-95: `messages.tool_calls` changed shape without a migration, so the
// reader is what keeps old rows readable. Every shape the column can hold is
// here — the list, the by-name map nothing wrote in practice but the type
// allowed, and the junk a JSONB column will happily store.
func TestToolCallListReadsEveryShapeTheColumnHolds(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want ToolCallList
	}{
		{
			name: "the list, in order, repeats kept",
			raw: `[{"name":"get_schema"},{"name":"run_sql","arguments":{"sql":"SELECT 1"}},` +
				`{"name":"run_sql","arguments":{"sql":"SELECT 2"},"result":{"status":"ok"}}]`,
			want: ToolCallList{
				{Name: "get_schema"},
				{Name: "run_sql", Arguments: map[string]interface{}{"sql": "SELECT 1"}},
				{Name: "run_sql", Arguments: map[string]interface{}{"sql": "SELECT 2"},
					Result: map[string]interface{}{"status": "ok"}},
			},
		},
		{
			name: "the legacy by-name map, one record per key in sorted order",
			raw:  `{"run_sql":{"sql":"SELECT 1"},"compute":{"expression":"a/b"},"get_schema":{}}`,
			want: ToolCallList{
				{Name: "compute", Result: map[string]interface{}{"expression": "a/b"}},
				{Name: "get_schema"},
				{Name: "run_sql", Result: map[string]interface{}{"sql": "SELECT 1"}},
			},
		},
		{name: "a legacy value that is not an object keeps the name", raw: `{"run_sql":"SELECT 1"}`,
			want: ToolCallList{{Name: "run_sql"}}},
		{name: "null", raw: `null`},
		{name: "a string", raw: `"x"`},
		{name: "a number", raw: `42`},
		{name: "an empty list", raw: `[]`},
		{name: "an empty map", raw: `{}`},
		{name: "a list of the wrong things", raw: `[1, 2]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got ToolCallList
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("unmarshal returned %v; a row nobody can render must still be served", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v\nwant %#v", got, tc.want)
			}
		})
	}
}

// Old rows reach the dashboard as a list, and a list goes back out as one.
func TestToolCallListMarshalsAsAList(t *testing.T) {
	var fromLegacy ToolCallList
	if err := json.Unmarshal([]byte(`{"run_sql":{"sql":"SELECT 1"}}`), &fromLegacy); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(Message{ID: "m-1", ToolCalls: fromLegacy})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		ToolCalls []map[string]interface{} `json:"tool_calls"`
	}
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatalf("tool_calls is not an array on the wire: %v (%s)", err, out)
	}
	if len(wire.ToolCalls) != 1 || wire.ToolCalls[0]["name"] != "run_sql" {
		t.Errorf("tool_calls = %v", wire.ToolCalls)
	}

	// The round trip of the new shape is lossless.
	list := ToolCallList{
		{Name: "run_sql", Arguments: map[string]interface{}{"sql": "SELECT 1"}},
		{Name: "run_sql", Result: map[string]interface{}{"url": "https://x"}},
	}
	raw, _ := json.Marshal(list)
	var back ToolCallList
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, list) {
		t.Errorf("round trip = %#v, %v; want %#v", back, err, list)
	}

	// No calls is no field at all, which is what every row before RIZ-95 served.
	empty, _ := json.Marshal(Message{ID: "m-2"})
	if string(empty) != `{"id":"m-2","thread_id":"","role":"","content":"","created_at":"0001-01-01T00:00:00Z"}` {
		t.Errorf("a message with no calls marshals as %s", empty)
	}
}
