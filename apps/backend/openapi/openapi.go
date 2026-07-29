// Package openapi is the published `/v1` contract and the code that reads it.
//
// The document itself is [v1.yaml], sitting beside this file. It is embedded
// rather than read from disk because it is served by a route: a container that
// shipped without its spec would answer 500 on `GET /v1/openapi.json`, and a
// missing file is a build failure here instead.
//
// Everything that consumes the contract goes through this package — the route
// that serves it, the parity test that diffs it against the router, the schema
// test that diffs it against the Go response structs, and the Postman
// generator. One reader, so "what does the spec say?" has one answer.
package openapi

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed v1.yaml
var specYAML []byte

// YAML returns the spec as authored. The bytes are the file's; callers must
// not modify them.
func YAML() []byte { return specYAML }

// httpMethods are the keys of a path item that describe an operation. The rest
// — `summary`, `parameters`, `servers` — are metadata about the path, and a
// reader that treated them as operations would report routes that do not
// exist.
var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// Operation is one method on one path, as the spec declares it.
type Operation struct {
	// Method is upper-case, matching what gin's RouteInfo reports, so the
	// parity check compares like with like.
	Method string
	// Path is the OpenAPI template — `/v1/threads/{id}`. GinPath converts it.
	Path string
	ID   string
	// Scope is the `x-argentum-scope` extension: the capability a key needs.
	// Empty means the route is reachable by any authenticated key, which is
	// true of `/v1/me` and of nothing else.
	Scope string
	// Public is true when the operation overrides the document's security with
	// an empty requirement — the spec route itself, and nothing else.
	Public bool
}

// Key renders an operation the way the route tables in `cmd/api` do, so a
// failure message can be pasted into a test that reads them.
func (o Operation) Key() string { return o.Method + " " + o.Path }

// document is the subset of the spec this package reads. It is deliberately
// not a full OpenAPI model: the parts nobody consults do not need types, and a
// partial decode of YAML ignores what it does not name.
type document struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
	Paths map[string]map[string]operation `yaml:"paths"`
}

type operation struct {
	OperationID string `yaml:"operationId"`
	Scope       string `yaml:"x-argentum-scope"`
	// Security is a pointer so "absent" (inherit the document's requirement)
	// is distinguishable from "present and empty" (public). A value type would
	// make every operation look public.
	Security *[]map[string][]string `yaml:"security"`
	Summary  string                 `yaml:"summary"`
}

var (
	parseOnce sync.Once
	parsed    document
	parseErr  error

	jsonOnce sync.Once
	jsonSpec []byte
	jsonErr  error
)

// parse decodes the embedded document once.
func parse() (document, error) {
	parseOnce.Do(func() {
		if err := yaml.Unmarshal(specYAML, &parsed); err != nil {
			parseErr = fmt.Errorf("parse openapi/v1.yaml: %w", err)
		}
	})
	return parsed, parseErr
}

// JSON returns the spec as JSON, converted once and cached.
//
// The conversion is here rather than a second checked-in file for the reason
// the design tokens learned: two copies of one truth disagree quietly. The
// authored form is YAML because a contract with this much prose in it is
// unreadable as JSON, and JSON is what `GET /v1/openapi.json` serves because
// that is what every generator reads.
func JSON() ([]byte, error) {
	jsonOnce.Do(func() {
		var raw any
		if err := yaml.Unmarshal(specYAML, &raw); err != nil {
			jsonErr = fmt.Errorf("parse openapi/v1.yaml: %w", err)
			return
		}
		// yaml.v3 decodes string-keyed mappings into map[string]any, which
		// encoding/json accepts directly. A non-string key would fail here
		// rather than silently, and there are none: the response codes are
		// quoted in the source for exactly that reason.
		jsonSpec, jsonErr = json.Marshal(raw)
	})
	return jsonSpec, jsonErr
}

// Version is `info.version` — the contract date `GET /v1/me` reports as
// `api_version`. A test pins the two equal, because an integrator comparing
// them and finding a difference has no way to know which one is lying.
func Version() (string, error) {
	doc, err := parse()
	if err != nil {
		return "", err
	}
	return doc.Info.Version, nil
}

// Operations returns every operation the spec declares, sorted by key.
func Operations() ([]Operation, error) {
	doc, err := parse()
	if err != nil {
		return nil, err
	}
	var out []Operation
	for path, item := range doc.Paths {
		for method, op := range item {
			if !httpMethods[strings.ToLower(method)] {
				continue
			}
			out = append(out, Operation{
				Method: strings.ToUpper(method),
				Path:   path,
				ID:     op.OperationID,
				Scope:  op.Scope,
				Public: op.Security != nil && len(*op.Security) == 0,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
}

// Schema returns one entry of `components.schemas`, decoded as the generic
// map a JSON Schema is.
//
// It is generic rather than typed because its consumer is the drift test,
// which walks properties by name — a Go type for a JSON Schema would be a
// third definition of the same shapes, which is the thing this whole package
// exists to prevent.
func Schema(name string) (map[string]any, error) {
	raw, err := JSON()
	if err != nil {
		return nil, err
	}
	var doc struct {
		Components struct {
			Schemas map[string]map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("read components.schemas: %w", err)
	}
	s, ok := doc.Components.Schemas[name]
	if !ok {
		return nil, fmt.Errorf("no such schema: components.schemas.%s", name)
	}
	return s, nil
}

// GinPath converts an OpenAPI path template to the pattern gin registers.
// `/v1/threads/{id}` becomes `/v1/threads/:id`.
//
// Only the two forms this API uses are handled — a named parameter spanning a
// whole segment. A catch-all or a partial-segment template would silently
// convert to something that never matches, so it is refused rather than
// guessed at.
func GinPath(specPath string) (string, error) {
	segs := strings.Split(specPath, "/")
	for i, seg := range segs {
		open := strings.Count(seg, "{")
		if open == 0 {
			if strings.Contains(seg, "}") {
				return "", fmt.Errorf("path %q: unbalanced braces in %q", specPath, seg)
			}
			continue
		}
		if open > 1 || !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			return "", fmt.Errorf("path %q: %q is not a whole-segment parameter", specPath, seg)
		}
		segs[i] = ":" + seg[1:len(seg)-1]
	}
	return strings.Join(segs, "/"), nil
}

// SpecPath is GinPath's inverse: `/v1/threads/:id` becomes
// `/v1/threads/{id}`. The parity check needs both directions so it can report
// a missing entry in the vocabulary of whichever side is missing it.
func SpecPath(ginPath string) string {
	segs := strings.Split(ginPath, "/")
	for i, seg := range segs {
		if strings.HasPrefix(seg, ":") {
			segs[i] = "{" + seg[1:] + "}"
		}
	}
	return strings.Join(segs, "/")
}
