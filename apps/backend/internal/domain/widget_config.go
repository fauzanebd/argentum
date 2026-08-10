package domain

import (
	"context"
	"fmt"
	"strings"
)

// WidgetConfig is what a tenant's embedded chat looks like and opens with
// (T-23).
//
// **Everything on it is public.** It is served to a browser on a page we do not
// control, by a route that authenticates a visitor rather than a member of
// staff — so nothing that is not already visible to that visitor may be added
// here. Not the credit position, not an agent's tools, not a source name. The
// test for a new field is: would we print it in the tenant's page source?
type WidgetConfig struct {
	// Greeting is the empty state's first line.
	Greeting string `json:"greeting,omitempty"`
	// SuggestedPrompts are the buttons under it. Three to five is the useful
	// range: fewer reads as an accident, more is a menu nobody scans.
	//
	// **No `omitempty`, unlike every other field here.** An empty slice with
	// it would be dropped from the JSON entirely, so a tenant with no prompts
	// would send the widget no key rather than an empty list — and a client
	// that then reads `config.suggested_prompts.length` gets a TypeError
	// instead of zero. A caught-by-test bug: the array is a contract, and a
	// present-but-empty array is what "none" looks like.
	SuggestedPrompts []string `json:"suggested_prompts"`
	// Locale is the default language of the widget's own chrome. The agent
	// answers in the language it was asked in regardless — this is the label on
	// the composer, not an instruction to the model.
	Locale string `json:"locale,omitempty"`
	// Primary is the accent colour, as a hex string. Radius is in pixels.
	Primary string `json:"primary,omitempty"`
	Radius  *int   `json:"radius,omitempty"`
	// Mode is light / dark / auto. Auto reads the visitor's own preference,
	// which is the right default for a widget that has to sit inside a host
	// page whose theme we cannot see.
	Mode string `json:"mode,omitempty"`
	// Launcher and Position control the bubble. `none` means the tenant renders
	// their own trigger and calls `Argentum.open()`.
	Launcher string `json:"launcher,omitempty"`
	Position string `json:"position,omitempty"`
}

// Widget config bounds. Small numbers, and each one is a limit on something a
// browser renders rather than on something we store: a 400px panel cannot show
// a 300-character greeting, and a tenant who sets one has made their own
// widget unusable rather than ours.
const (
	widgetGreetingMax = 200
	widgetPromptMax   = 120
	widgetPromptCount = 5
)

// DefaultWidgetGreeting is what a tenant who has configured nothing gets. In
// Go rather than seeded into the column, so changing it is a deploy and not a
// backfill.
const DefaultWidgetGreeting = "Ask me about your data."

// Normalize validates and tidies a config on the way in. It is deliberately
// forgiving about *absence* — every field is optional and an empty config is
// the ordinary state — and strict about content, because everything here is
// rendered into a page.
func (w *WidgetConfig) Normalize() error {
	w.Greeting = strings.TrimSpace(w.Greeting)
	if len(w.Greeting) > widgetGreetingMax {
		return fmt.Errorf("%w: the greeting must be %d characters or fewer", ErrInvalidInput, widgetGreetingMax)
	}

	prompts := make([]string, 0, len(w.SuggestedPrompts))
	for _, p := range w.SuggestedPrompts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len(p) > widgetPromptMax {
			return fmt.Errorf("%w: a suggested prompt must be %d characters or fewer", ErrInvalidInput, widgetPromptMax)
		}
		prompts = append(prompts, p)
	}
	if len(prompts) > widgetPromptCount {
		return fmt.Errorf("%w: at most %d suggested prompts", ErrInvalidInput, widgetPromptCount)
	}
	w.SuggestedPrompts = prompts

	if w.Primary != "" && !isHexColor(w.Primary) {
		// Rejected rather than dropped: this string becomes a CSS value in
		// somebody else's page, and the failure mode of accepting arbitrary
		// text there is a style injection rather than a wrong colour.
		return fmt.Errorf("%w: the accent colour must be a hex value like #e11d48", ErrInvalidInput)
	}
	if w.Radius != nil && (*w.Radius < 0 || *w.Radius > 32) {
		return fmt.Errorf("%w: the corner radius must be between 0 and 32", ErrInvalidInput)
	}
	if err := oneOf("mode", w.Mode, "light", "dark", "auto"); err != nil {
		return err
	}
	if err := oneOf("launcher", w.Launcher, "bubble", "none"); err != nil {
		return err
	}
	if err := oneOf("position", w.Position, "bottom-right", "bottom-left"); err != nil {
		return err
	}
	if err := oneOf("locale", w.Locale, "en", "id"); err != nil {
		return err
	}
	return nil
}

// WithDefaults returns the config the widget should actually render with:
// whatever the tenant set, and Argentum's own answer everywhere they set
// nothing. Applied on read rather than stored on write, so a default that
// changes reaches every tenant who never overrode it.
func (w WidgetConfig) WithDefaults() WidgetConfig {
	if w.Greeting == "" {
		w.Greeting = DefaultWidgetGreeting
	}
	if w.SuggestedPrompts == nil {
		w.SuggestedPrompts = []string{}
	}
	if w.Locale == "" {
		w.Locale = "en"
	}
	if w.Mode == "" {
		w.Mode = "auto"
	}
	if w.Launcher == "" {
		w.Launcher = "bubble"
	}
	if w.Position == "" {
		w.Position = "bottom-right"
	}
	return w
}

// oneOf accepts an empty value — every field here is optional — and otherwise
// requires membership.
func oneOf(field, value string, allowed ...string) error {
	if value == "" {
		return nil
	}
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("%w: %s must be one of %s", ErrInvalidInput, field, strings.Join(allowed, ", "))
}

// isHexColor accepts `#rgb` and `#rrggbb`, and nothing else.
func isHexColor(s string) bool {
	if len(s) != 4 && len(s) != 7 {
		return false
	}
	if s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		isDigit := c >= '0' && c <= '9'
		isLower := c >= 'a' && c <= 'f'
		isUpper := c >= 'A' && c <= 'F'
		if !isDigit && !isLower && !isUpper {
			return false
		}
	}
	return true
}

// WidgetConfigStore is the persistence contract, declared beside the type it
// stores. *postgres.CompanyRepo satisfies it.
type WidgetConfigStore interface {
	GetWidgetConfig(ctx context.Context, companyID string) (*WidgetConfig, error)
	SaveWidgetConfig(ctx context.Context, companyID string, c *WidgetConfig) error
}
