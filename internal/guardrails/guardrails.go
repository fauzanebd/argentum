package guardrails

import (
	"context"
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Config is the top-level guardrails configuration loaded from YAML.
type Config struct {
	Version int    `yaml:"version"`
	Rules   []Rule `yaml:"rules"`
}

// Rule represents a single guardrail rule.
type Rule struct {
	Name        string    `yaml:"name"`
	Description string    `yaml:"description"`
	// Scope controls when the rule is applied: "input" (user messages only),
	// "output" (agent responses only), or "" / omitted (both).
	Scope       string    `yaml:"scope"`
	Patterns    []Pattern `yaml:"patterns"`
	// Action is one of: "block" (block if any pattern matches),
	// "require" (block if NO pattern matches), "redact", "filter".
	Action      string    `yaml:"action"`
	Message     string    `yaml:"message"`
	Replacement string    `yaml:"replacement"`
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
}

type compiledRule struct {
	rule     Rule
	patterns []*regexp.Regexp
}

// LoadFromFile creates an Analytics guardrails instance from a YAML config file.
func LoadFromFile(path string) (*Analytics, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read guardrails config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse guardrails config: %w", err)
	}

	return New(cfg)
}

// New creates an Analytics guardrails instance from a Config struct.
func New(cfg Config) (*Analytics, error) {
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
			}
		}
		compiled = append(compiled, cr)
	}
	return &Analytics{config: cfg, compiledRules: compiled}, nil
}

// ProcessInput checks user input against guardrail rules.
// Implements interfaces.Guardrails.
func (a *Analytics) ProcessInput(ctx context.Context, input string) (string, error) {
	return a.process(input, "input")
}

// ProcessOutput checks agent output against guardrail rules.
// Implements interfaces.Guardrails.
func (a *Analytics) ProcessOutput(ctx context.Context, output string) (string, error) {
	return a.process(output, "output")
}

func (a *Analytics) process(text string, stage string) (string, error) {
	result := text
	for _, cr := range a.compiledRules {
		// Skip rules scoped to a different stage.
		if cr.rule.Scope != "" && cr.rule.Scope != stage {
			continue
		}

		switch cr.rule.Action {
		case "block":
			for _, re := range cr.patterns {
				if re.MatchString(result) {
					msg := cr.rule.Message
					if msg == "" {
						msg = fmt.Sprintf("blocked by guardrail: %s", cr.rule.Name)
					}
					return "", fmt.Errorf("%s", msg)
				}
			}

		case "require":
			// Block when NONE of the patterns match — used for topic enforcement.
			anyMatch := false
			for _, re := range cr.patterns {
				if re.MatchString(result) {
					anyMatch = true
					break
				}
			}
			if !anyMatch {
				msg := cr.rule.Message
				if msg == "" {
					msg = "I can only help with analytics and business intelligence questions."
				}
				return "", fmt.Errorf("%s", msg)
			}

		case "redact":
			replacement := cr.rule.Replacement
			if replacement == "" {
				replacement = "[REDACTED]"
			}
			for _, re := range cr.patterns {
				result = re.ReplaceAllString(result, replacement)
			}

		case "filter":
			replacement := cr.rule.Replacement
			if replacement == "" {
				replacement = "****"
			}
			for _, re := range cr.patterns {
				result = re.ReplaceAllString(result, replacement)
			}
		}
	}
	return result, nil
}
