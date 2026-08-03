// Package guardrails is the input/output policy layer for the analytics
// agent: a YAML rule set of regex and LLM-classifier patterns that blocks,
// requires or redacts, plus the T-16 fabrication check that runs outside it.
package guardrails

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"gopkg.in/yaml.v3"
)

// Config is the top-level guardrails configuration loaded from YAML.
type Config struct {
	Version int    `yaml:"version"`
	Rules   []Rule `yaml:"rules"`
}

// Rule represents a single guardrail rule.
type Rule struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// Scope controls when the rule is applied: "input" (user messages only),
	// "output" (agent responses only), or "" / omitted (both).
	Scope string `yaml:"scope"`
	// PIIClass says what kind of personal data this rule removes, and is what
	// a tenant's PIIMode switches off. "contact" is an email address or a phone
	// number — the fields a customer list is *made of*, and the ones a tenant
	// asking for one has to be able to see. "identity" is a national id, a
	// social security number or a card number, which no BI question needs and
	// only `off` removes.
	//
	// Empty means the rule is not PII policy at all and no mode touches it:
	// block_system_prompt_leak is an output rule a tenant may not switch off,
	// because it protects Argentum rather than the tenant's own data.
	PIIClass string    `yaml:"pii_class"`
	Patterns []Pattern `yaml:"patterns"`
	// Action is one of: "block" (block if any pattern matches),
	// "require" (block if NO pattern matches), "redact", "filter".
	Action      string `yaml:"action"`
	Message     string `yaml:"message"`
	MessageEN   string `yaml:"message_en"`
	MessageID   string `yaml:"message_id"`
	Replacement string `yaml:"replacement"`
}

// Pattern represents a matching pattern within a rule.
type Pattern struct {
	Type    string `yaml:"type"`
	Pattern string `yaml:"pattern"`
}

// Analytics implements interfaces.Guardrails for the Argentum analytics agent.
// It loads rules from a YAML config and applies regex-based checks to both
// input (user queries) and output (agent responses).
type Analytics struct {
	config        Config
	compiledRules []compiledRule
	llm           interfaces.LLM
}

type compiledRule struct {
	rule     Rule
	patterns []*regexp.Regexp
}

// LoadFromFile creates an Analytics guardrails instance from a YAML config file.
func LoadFromFile(path string, llm interfaces.LLM) (*Analytics, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read guardrails config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse guardrails config: %w", err)
	}

	return New(cfg, llm)
}

// WithLLM returns a shallow copy of the Analytics bound to a different
// light LLM. Used by the per-turn agent factory to rebind without
// re-reading YAML or recompiling regex patterns.
func (a *Analytics) WithLLM(llm interfaces.LLM) *Analytics {
	cp := *a
	cp.llm = llm
	return &cp
}

// New creates an Analytics guardrails instance from a Config struct.
func New(cfg Config, llm interfaces.LLM) (*Analytics, error) {
	compiled := make([]compiledRule, 0, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		cr := compiledRule{rule: rule}
		for _, p := range rule.Patterns {
			if p.Type == "regex" {
				re, err := regexp.Compile(p.Pattern)
				if err != nil {
					return nil, fmt.Errorf("invalid regex in rule %q: %w", rule.Name, err)
				}
				cr.patterns = append(cr.patterns, re)
			} else {
				cr.patterns = append(cr.patterns, nil)
			}
		}
		compiled = append(compiled, cr)
	}
	return &Analytics{config: cfg, compiledRules: compiled, llm: llm}, nil
}

// PIIMode is a tenant's policy for the redaction rules, stored on the company
// row and passed in per turn (T-07b).
//
// The rules themselves are the same for everybody; what a mode decides is which
// of them run. A tenant whose agent exists to pull a customer contact list
// cannot work under a policy that blanks every email address in the answer, and
// a tenant in a regulated industry needs exactly that policy — so this is a
// setting rather than a pattern anyone can tune correctly for both.
type PIIMode string

const (
	// PIIStrict redacts everything the rules can find. The default, and what a
	// company with no explicit setting gets.
	PIIStrict PIIMode = "strict"
	// PIIContactOK lets email addresses and phone numbers through. National
	// ids, SSNs and card numbers are still redacted: "my staff may read our
	// customers' contact details" is a different statement from "my staff may
	// read our customers' identity documents", and one does not imply the other.
	PIIContactOK PIIMode = "contact_ok"
	// PIIOff runs no redaction rule at all. Blocking output rules still apply —
	// a mode is a policy over the tenant's own data, not a switch that turns the
	// output stage off.
	PIIOff PIIMode = "off"
)

// Normalize maps anything unrecognised — including the empty string a row
// written before migration 045 carries — onto strict. The unknown value is the
// one case where guessing wrong matters, and over-redacting is the recoverable
// direction.
func (m PIIMode) Normalize() PIIMode {
	switch m {
	case PIIContactOK, PIIOff:
		return m
	default:
		return PIIStrict
	}
}

// skips reports whether this mode switches off a rule of the given class.
func (m PIIMode) skips(class string) bool {
	if class == "" {
		return false
	}
	switch m.Normalize() {
	case PIIContactOK:
		return class == "contact"
	case PIIOff:
		return true
	default:
		return false
	}
}

// ProcessInput checks user input against guardrail rules.
// Implements interfaces.Guardrails.
func (a *Analytics) ProcessInput(ctx context.Context, input string) (string, error) {
	return a.process(ctx, input, "input", input, PIIStrict)
}

// ProcessOutput checks agent output against guardrail rules.
// Implements interfaces.Guardrails.
//
// This is the SDK's entry point, which reaches it only on the blocking path, so
// it has no company to read a mode from and runs the strictest one. Callers that
// know whose turn this is — ChatRunner, on the streaming path every chat turn
// actually takes — use ProcessOutputFor instead.
func (a *Analytics) ProcessOutput(ctx context.Context, output string) (string, error) {
	return a.process(ctx, output, "output", "", PIIStrict)
}

// ProcessOutputFor applies the output-scope rules under one company's PII
// policy.
func (a *Analytics) ProcessOutputFor(ctx context.Context, output string, mode PIIMode) (string, error) {
	return a.process(ctx, output, "output", "", mode)
}

// resolveMessage picks the right language-specific message for a rule based
// on the original user input. Falls back to the generic Message field.
func resolveMessage(rule Rule, userInput string, fallback string) string {
	if userInput != "" && rule.MessageID != "" && rule.MessageEN != "" {
		if looksIndonesian(userInput) {
			return rule.MessageID
		}
		return rule.MessageEN
	}
	if rule.Message != "" {
		return rule.Message
	}
	return fallback
}

// looksIndonesian returns true if the text contains common Indonesian words.
var indonesianMarkers = regexp.MustCompile(
	`(?i)\b(saya|aku|kamu|anda|bisa|tidak|apa|siapa|bagaimana|mengapa|kenapa|` +
		`tolong|bantu|terima kasih|makasih|halo|hai|selamat|mohon|mau|ingin|` +
		`berapa|dimana|kapan|silakan|data|tampilkan|tunjukkan|cari|hitung|` +
		`bulan|tahun|minggu|hari|kemarin|lalu|tren|laporan|dasbor)\b`)

func looksIndonesian(text string) bool {
	return indonesianMarkers.MatchString(strings.ToLower(text))
}

func (a *Analytics) process(ctx context.Context, text string, stage string, userInput string, mode PIIMode) (string, error) {
	result := text
	for _, cr := range a.compiledRules {
		// Skip rules scoped to a different stage.
		if cr.rule.Scope != "" && cr.rule.Scope != stage {
			continue
		}
		// Skip rules this company's PII policy switches off.
		if mode.skips(cr.rule.PIIClass) {
			continue
		}

		switch cr.rule.Action {
		case "block":
			for i, p := range cr.rule.Patterns {
				if p.Type == "regex" {
					re := cr.patterns[i]
					if re != nil && re.MatchString(result) {
						msg := resolveMessage(cr.rule, userInput, fmt.Sprintf("blocked by guardrail: %s", cr.rule.Name))
						return "", fmt.Errorf("%s", msg)
					}
				} else if p.Type == "llm" && a.llm != nil {
					resp, err := a.llm.Generate(ctx, result,
						interfaces.WithSystemMessage(p.Pattern),
						interfaces.WithTemperature(0),
					)
					if err == nil && strings.Contains(strings.ToUpper(strings.TrimSpace(resp)), "TRUE") {
						msg := resolveMessage(cr.rule, userInput, fmt.Sprintf("blocked by semantic guardrail: %s", cr.rule.Name))
						return "", fmt.Errorf("%s", msg)
					}
				}
			}

		case "require":
			// Block when NONE of the patterns match — used for topic enforcement.
			anyMatch := false
			for i, p := range cr.rule.Patterns {
				if p.Type == "regex" {
					re := cr.patterns[i]
					if re != nil && re.MatchString(result) {
						anyMatch = true
						break
					}
				} else if p.Type == "llm" && a.llm != nil {
					resp, err := a.llm.Generate(ctx, result,
						interfaces.WithSystemMessage(p.Pattern),
						interfaces.WithTemperature(0),
					)
					if err != nil {
						// Fail-closed for topic enforcement: do not admit arbitrary input when the classifier is unavailable.
						continue
					}
					if strings.Contains(strings.ToUpper(strings.TrimSpace(resp)), "TRUE") {
						anyMatch = true
						break
					}
				}
			}
			if !anyMatch {
				msg := resolveMessage(cr.rule, userInput, "I can only help with analytics and business intelligence questions.")
				return "", fmt.Errorf("%s", msg)
			}

		case "redact":
			replacement := cr.rule.Replacement
			if replacement == "" {
				replacement = "[REDACTED]"
			}
			for i, p := range cr.rule.Patterns {
				if p.Type == "regex" {
					re := cr.patterns[i]
					if re != nil {
						result = re.ReplaceAllString(result, replacement)
					}
				}
			}

		case "filter":
			replacement := cr.rule.Replacement
			if replacement == "" {
				replacement = "****"
			}
			for i, p := range cr.rule.Patterns {
				if p.Type == "regex" {
					re := cr.patterns[i]
					if re != nil {
						result = re.ReplaceAllString(result, replacement)
					}
				}
			}
		}
	}
	return result, nil
}
