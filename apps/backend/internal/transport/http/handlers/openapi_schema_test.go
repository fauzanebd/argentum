package handlers

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
	"github.com/fauzanebd/argentum/internal/transport/http/apiv1"
	"github.com/fauzanebd/argentum/openapi"
)

// The other half of T-A4's drift guard.
//
// `cmd/api`'s parity test proves the spec lists the right *routes*. Nothing
// there looks inside a response, so a field renamed in Go — or a property
// invented in the spec that no handler ever writes — would ship as a published
// promise nobody keeps. That is the same "two copies of one truth, disagreeing
// quietly" failure the design tokens had, and it gets the same treatment: read
// both copies, diff them, fail in both directions.
//
// This lives in `handlers` rather than beside the spec because most of the
// response types are unexported, which is correct — they are the wire format,
// not an API for other packages — and a test in the same package can still see
// them.

// schemaCase pairs one Go type with the schema that claims to describe it.
type schemaCase struct {
	// schema is the key under `components.schemas`.
	schema string
	// value is a zero value of the type; only its fields are read.
	value any
	// request marks a body the server *decodes*. On those, `omitempty` says
	// nothing about whether a caller has to send the field — `createReportRequest`
	// carries `format` without it and still defaults to PDF — so only the
	// property names are compared. Requiredness on a request body is a
	// validation rule in the handler, and the handler's own tests own it.
	request bool
}

var schemaCases = []schemaCase{
	{schema: "Error", value: apierr.Body{}},
	{schema: "ErrorDetail", value: apierr.Detail{}},
	{schema: "Document", value: documentResponse{}},
	{schema: "Report", value: reportResponse{}},
	{schema: "Turn", value: turnResponse{}},
	{schema: "Message", value: messageResponse{}},
	{schema: "Thread", value: threadResponse{}},
	{schema: "Usage", value: usageBody{}},
	{schema: "PendingTurn", value: pendingBody{}},
	// One generic type behind three schemas. They are three rather than one
	// `$ref` because a generated client should name `DocumentPage.data` as an
	// array of documents, not as an array of "T".
	{schema: "DocumentPage", value: apiv1.Page[documentResponse]{}},
	{schema: "MessagePage", value: apiv1.Page[messageResponse]{}},
	{schema: "ThreadPage", value: apiv1.Page[threadResponse]{}},

	{schema: "ChatRequest", value: chatRequest{}, request: true},
	{schema: "CreateReportRequest", value: createReportRequest{}, request: true},

	// The render door's body is `spec.Document` itself — decoded straight into
	// the type the renderers read, with no wrapper, so the spec has to describe
	// that type and not a paraphrase of it.
	{schema: "ReportSpec", value: spec.Document{}, request: true},
	{schema: "ReportSpecMeta", value: spec.Meta{}, request: true},
	{schema: "ReportSpecContent", value: spec.Content{}, request: true},
	{schema: "ReportSpecSection", value: spec.Section{}, request: true},
	{schema: "ReportSpecItem", value: spec.Item{}, request: true},
	{schema: "ReportSpecTable", value: spec.Table{}, request: true},
	{schema: "ReportSpecSheet", value: spec.Sheet{}, request: true},
	{schema: "ReportSpecChart", value: spec.Chart{}, request: true},
	{schema: "ReportSpecSeries", value: spec.Series{}, request: true},
	{schema: "ReportSpecAxis", value: spec.AxisSpec{}, request: true},
}

// TestSpecSchemasMatchTheGoStructs is the diff.
func TestSpecSchemasMatchTheGoStructs(t *testing.T) {
	for _, tc := range schemaCases {
		t.Run(tc.schema, func(t *testing.T) {
			schema, err := openapi.Schema(tc.schema)
			if err != nil {
				t.Fatalf("%v", err)
			}
			assertShape(t, tc.schema, schema, tc.value, tc.request)
		})
	}
}

// TestSpecSchemasWithBothShapes covers the two spec types that accept either a
// scalar or an object, where the object branch is what a Go struct describes.
//
// They are `oneOf` in the document because they are `UnmarshalJSON` in Go: a
// bare string column and a bare scalar cell are the v1 shape, and a v1 spec is
// still a valid v2 spec. A generated client that only knew the object form
// would refuse examples this API accepts.
func TestSpecSchemasWithBothShapes(t *testing.T) {
	for _, tc := range []schemaCase{
		{schema: "ReportSpecColumn", value: spec.Column{}},
		{schema: "ReportSpecCell", value: spec.Cell{}},
	} {
		t.Run(tc.schema, func(t *testing.T) {
			schema, err := openapi.Schema(tc.schema)
			if err != nil {
				t.Fatalf("%v", err)
			}
			branches, ok := schema["oneOf"].([]any)
			if !ok || len(branches) != 2 {
				t.Fatalf("%s should be a oneOf over the scalar and object forms", tc.schema)
			}
			var object map[string]any
			for _, b := range branches {
				branch, ok := b.(map[string]any)
				if !ok {
					continue
				}
				if branch["type"] == "object" {
					object = branch
				}
			}
			if object == nil {
				t.Fatalf("%s has no object branch to compare against %T", tc.schema, tc.value)
			}
			assertShape(t, tc.schema+"/oneOf[object]", object, tc.value, true)
		})
	}
}

// TestPendingTurnInFlightMatchesTheRecord reaches one level in, because the
// 504's `in_flight` is an inline object rather than a named schema — and it is
// the field a client reads to find the turn it is still owed.
func TestPendingTurnInFlightMatchesTheRecord(t *testing.T) {
	schema, err := openapi.Schema("PendingTurn")
	if err != nil {
		t.Fatalf("%v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("PendingTurn has no properties")
	}
	inFlight, ok := props["in_flight"].(map[string]any)
	if !ok {
		t.Fatal("PendingTurn has no in_flight property")
	}
	assertShape(t, "PendingTurn.in_flight", inFlight, turnRecord{}, false)
}

// assertShape compares one schema object against one Go struct.
func assertShape(t *testing.T, name string, schema map[string]any, value any, request bool) {
	t.Helper()

	props := map[string]bool{}
	if raw, ok := schema["properties"].(map[string]any); ok {
		for k := range raw {
			props[k] = true
		}
	}
	if len(props) == 0 {
		t.Fatalf("%s declares no properties", name)
	}
	required := map[string]bool{}
	if raw, ok := schema["required"].([]any); ok {
		for _, r := range raw {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}

	fields := jsonFields(reflect.TypeOf(value))
	for _, f := range sortedKeys(fields) {
		if !props[f] {
			t.Errorf("%s: the Go type writes %q, the spec does not declare it — "+
				"a field a client cannot see is a field it will not read", name, f)
		}
	}
	for _, p := range sortedKeys(props) {
		if _, ok := fields[p]; !ok {
			t.Errorf("%s: the spec declares %q, no Go field carries it — "+
				"a promised field that is never written is worse than an absent one", name, p)
		}
	}

	if request {
		return
	}
	// On a response, `omitempty` *is* the contract: a field that can be absent
	// must not be required, and one that is always written must be, or a
	// generated client makes it optional and every consumer writes a nil check
	// for a value that is always there.
	for _, f := range sortedKeys(fields) {
		always := fields[f]
		if always && !required[f] {
			t.Errorf("%s: %q is always written but the spec does not require it", name, f)
		}
		if !always && required[f] {
			t.Errorf("%s: %q is `omitempty` in Go but required by the spec — "+
				"a client will fail to parse a response that omits it", name, f)
		}
	}
}

// jsonFields maps a struct's JSON field names to whether the field is always
// present in the encoded object — that is, whether it lacks `omitempty` and
// `omitzero`.
func jsonFields(rt reflect.Type) map[string]bool {
	out := map[string]bool{}
	for f := range rt.Fields() {
		if f.PkgPath != "" {
			continue // unexported: never encoded
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		out[name] = !strings.Contains(opts, "omitempty") && !strings.Contains(opts, "omitzero")
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
