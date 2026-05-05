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
	Scope    string    `yaml:"scope"`
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

// ProcessInput checks user input against guardrail rules.
// Implements interfaces.Guardrails.
func (a *Analytics) ProcessInput(ctx context.Context, input string) (string, error) {
	return a.process(ctx, input, "input", input)
}

// ProcessOutput checks agent output against guardrail rules.
// Implements interfaces.Guardrails.
func (a *Analytics) ProcessOutput(ctx context.Context, output string) (string, error) {
	return a.process(ctx, output, "output", "")
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

func (a *Analytics) process(ctx context.Context, text string, stage string, userInput string) (string, error) {
	result := text
	for _, cr := range a.compiledRules {
		// Skip rules scoped to a different stage.
		if cr.rule.Scope != "" && cr.rule.Scope != stage {
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
						interfaces.WithStopSequences([]string{"\n"}),
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
						interfaces.WithStopSequences([]string{"\n"}),
					)
					if err != nil {
						// Fail-open: do not strand users behind topic guard when the classifier fails.
						anyMatch = true
						break
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
