// Package agenttemplates loads config/agent_templates.yaml: the six starting
// points an admin picks from instead of writing a system prompt (T-B3).
//
// Templates are code, not tenant rows (locked decision 4). That is what makes
// them safe to ship before we know what a good persona looks like in
// production: a guess that turns out wrong is a one-line commit that reaches
// every tenant who has not edited theirs, rather than a migration that cannot
// reach the tenant who has. `config/guardrails.yaml` is the prior art, down to
// the golden test over the real file.
//
// Nothing in a template survives the save except the key. A created agent is an
// ordinary roster row that T-S2 runs; there is no inheritance, no live link and
// no "update all agents from template", so editing this file changes no agent
// that already exists.
package agenttemplates

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Template is one entry in the gallery.
//
// It carries the shape of a *job* and never a claim about an industry — the
// business specifics reach the prompt from the company profile (T-B1) at turn
// time. A template that described a retailer would be wrong for the next tenant
// who picked it, and nothing would tell them.
type Template struct {
	Key         string `yaml:"key" json:"key"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	// Persona prefills the form's instructions field. It is the tenant's text
	// from the moment they save — editable before the save and never rewritten
	// after it.
	Persona string `yaml:"persona" json:"persona"`
	// SuggestedTools pre-ticks checkboxes. Validated at load against every tool
	// this release knows, then narrowed per deployment by ForRegistry: an entry
	// no registry knows is a boot failure, one this deployment happens not to
	// run is simply absent.
	SuggestedTools []string `yaml:"suggested_tools" json:"suggested_tools"`
	// SourceHints pre-tick likely databases. Matched at the word start,
	// case-insensitively, by the dashboard against a connection's label and
	// description — a hint that is always editable, never a filter.
	SourceHints []string `yaml:"source_hints" json:"source_hints"`
	// StarterQuestions are shown on a new conversation opened on this agent:
	// the cheapest possible proof that the agent works.
	StarterQuestions []string `yaml:"starter_questions" json:"starter_questions"`
}

// Config is the top-level file shape.
type Config struct {
	Version   int        `yaml:"version"`
	Templates []Template `yaml:"templates"`
}

// Set is a validated, immutable gallery.
type Set struct{ templates []Template }

// LoadFromFile reads and validates the gallery. knownTools is every tool name
// this release can register (tools.AllNames), not this deployment's registry —
// validating against the live one would make a deployment without object
// storage refuse to boot over a template that suggests generate_document, which
// is exactly the case ForRegistry exists to handle gracefully.
//
// The error is returned rather than warned about, and the caller is expected to
// refuse to start: a malformed gallery is a file a developer just edited, and
// booting without it would show every tenant an empty create screen with
// nothing in the logs anybody reads.
func LoadFromFile(path string, knownTools []string) (*Set, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read agent templates: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse agent templates: %w", err)
	}
	s, err := New(cfg, knownTools)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// New validates a parsed config into a Set.
//
// Every failure names the offending template, because the reader is somebody
// who just edited one line of YAML and needs to know which line.
func New(cfg Config, knownTools []string) (*Set, error) {
	if len(cfg.Templates) == 0 {
		return nil, fmt.Errorf("no templates defined")
	}
	seen := map[string]bool{}
	out := make([]Template, 0, len(cfg.Templates))
	for i, t := range cfg.Templates {
		t.Key = strings.TrimSpace(t.Key)
		t.Name = strings.TrimSpace(t.Name)
		t.Description = strings.TrimSpace(t.Description)
		t.Persona = strings.TrimSpace(t.Persona)

		switch {
		case t.Key == "":
			return nil, fmt.Errorf("template %d has no key", i)
		case seen[t.Key]:
			// A duplicate would resolve to whichever copy the lookup reached
			// first, so the tenant would get one of two personas with no way to
			// tell which.
			return nil, fmt.Errorf("template %q is defined twice", t.Key)
		case t.Name == "":
			return nil, fmt.Errorf("template %q has no name", t.Key)
		case t.Persona == "":
			// An empty persona is the empty textarea this whole ticket exists
			// to replace. A template that prefills nothing is a card that lies.
			return nil, fmt.Errorf("template %q has no persona", t.Key)
		}
		for _, tool := range t.SuggestedTools {
			if !slices.Contains(knownTools, tool) {
				return nil, fmt.Errorf("template %q suggests unknown tool %q", t.Key, tool)
			}
		}
		seen[t.Key] = true
		out = append(out, t)
	}
	return &Set{templates: out}, nil
}

// All returns the gallery in file order. The order is the order the cards
// render in, so it is the file's decision rather than a map's.
func (s *Set) All() []Template {
	if s == nil {
		return nil
	}
	return slices.Clone(s.templates)
}

// Keys is what a submitted template_key is validated against.
func (s *Set) Keys() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.templates))
	for _, t := range s.templates {
		out = append(out, t.Key)
	}
	return out
}

// Has reports whether a key names a template in this gallery.
func (s *Set) Has(key string) bool {
	if s == nil {
		return false
	}
	return slices.ContainsFunc(s.templates, func(t Template) bool { return t.Key == key })
}

// ForRegistry returns the gallery with each template's SuggestedTools narrowed
// to the tools this deployment actually runs.
//
// This is the difference between "the release knows this tool" and "this
// process registered it": generate_document exists only where object storage
// does, and a card that pre-ticks it on a deployment without one produces a
// form that fails on first save with an error about a tool the admin never
// chose.
func (s *Set) ForRegistry(available []string) []Template {
	if s == nil {
		return nil
	}
	out := make([]Template, 0, len(s.templates))
	for _, t := range s.templates {
		cp := t
		cp.SuggestedTools = make([]string, 0, len(t.SuggestedTools))
		for _, tool := range t.SuggestedTools {
			if slices.Contains(available, tool) {
				cp.SuggestedTools = append(cp.SuggestedTools, tool)
			}
		}
		out = append(out, cp)
	}
	return out
}
