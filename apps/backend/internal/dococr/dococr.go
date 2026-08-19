// Package dococr reads a rendered page with a multimodal model (T-P3).
//
// **It is off by default and it is the only part of this roadmap that sends a
// tenant's document out of the deployment.** `T-P2` leaves scanned pages empty,
// which is honest and useless to a tenant whose supplier sends scans; this is
// what turns those pages into text. A rendered page leaving for a third-party
// model is exactly what `LLM_ZDR` was shipped to let an operator control, so
// `DOC_OCR_ENABLED` defaults to false and nothing here runs until somebody
// decides otherwise.
//
// **Why a client of its own rather than the agent's LLM interface.** The SDK's
// `interfaces.LLM` takes a string prompt and returns a string; there is no
// place in it for an image. So this is a small, direct OpenAI-compatible
// chat-completions call — the shape every provider this deployment talks to
// serves — and it reports its token usage back so the parse appears in the
// ledger like every other model call. A parse that spends money and does not
// appear in the ledger is a bill nobody can explain.
//
// One page at a time, always. A model shown two pages will reconcile them, and
// a model that reconciles is a model that invents: the continuation logic that
// joins a table across pages is deterministic on purpose (`internal/doctable`),
// and it cannot stay deterministic if the text it works on was already merged
// by a model.
package dococr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ErrNotConfigured means OCR is off, or has no model or credentials. A
// supported state, and the default one.
var ErrNotConfigured = errors.New("document OCR is not configured on this deployment")

// Options configures the client.
type Options struct {
	// BaseURL is an OpenAI-compatible endpoint — the deployment's own LLM host.
	// The path `/chat/completions` is appended, so pass the root.
	BaseURL string
	APIKey  string
	// Model is the multimodal model to read pages with. Named rather than
	// defaulted: which model reads a tenant's bank statement is an operator's
	// decision, and a default here would make it ours.
	Model   string
	Timeout time.Duration
}

// Client reads one page at a time.
type Client struct {
	base    string
	key     string
	model   string
	http    *http.Client
	timeout time.Duration
}

// New returns a client, or nil when this deployment has not configured one.
// Nil is legal at every call site: the methods are nil-safe.
func New(o Options) *Client {
	if strings.TrimSpace(o.BaseURL) == "" || strings.TrimSpace(o.Model) == "" {
		return nil
	}
	if o.Timeout <= 0 {
		o.Timeout = 90 * time.Second
	}
	return &Client{
		base:    strings.TrimRight(o.BaseURL, "/"),
		key:     o.APIKey,
		model:   o.Model,
		http:    &http.Client{},
		timeout: o.Timeout,
	}
}

// Configured reports whether a page can be read here.
func (c *Client) Configured() bool { return c != nil && c.model != "" }

// Model is which model read the page. Recorded on the artifact and by T-P13
// beside every score, for T-Q15's reason: a number that cannot name what
// produced it cannot be re-run as the same measurement.
func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

// Usage is what one page cost, in the provider's own counting.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// ReadPage returns the text of one rendered page.
//
// The prompt is deliberately narrow: transcribe, preserve layout, invent
// nothing, and say nothing about what the page means. Every word of
// interpretation a model adds here is a word `internal/doctable` will later
// treat as the document's own — and this is the one path in the roadmap where
// the document's own text is not available to contradict it.
func (c *Client) ReadPage(ctx context.Context, contentType, base64Image string) (string, Usage, error) {
	if !c.Configured() {
		return "", Usage{}, ErrNotConfigured
	}
	if base64Image == "" {
		return "", Usage{}, fmt.Errorf("no image to read")
	}
	if contentType == "" {
		contentType = "image/png"
	}

	body, err := json.Marshal(map[string]any{
		"model":       c.model,
		"temperature": 0,
		"messages": []any{
			map[string]any{
				"role":    "system",
				"content": systemPrompt,
			},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": userPrompt},
					map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url": "data:" + contentType + ";base64," + base64Image,
						},
					},
				},
			},
		},
	})
	if err != nil {
		return "", Usage{}, fmt.Errorf("build OCR request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, fmt.Errorf("build OCR request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", Usage{}, fmt.Errorf("call OCR model: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// The body is read with a cap and *not* logged by this package: an error
		// body from a provider can echo the request, and the request holds a
		// page of somebody's bank statement.
		return "", Usage{}, fmt.Errorf("OCR model answered %d", resp.StatusCode)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
		// Model as the provider resolved it, which is not always the model that
		// was asked for. T-Q15's whole argument: a published number that names
		// an unpinned alias cannot be re-run as the same measurement.
		Model string `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", Usage{}, fmt.Errorf("read OCR answer: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", out.Usage, fmt.Errorf("the OCR model returned nothing for this page")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), out.Usage, nil
}

const systemPrompt = "You transcribe scanned business documents. You reproduce exactly what is " +
	"printed on the page and nothing else: no summary, no explanation, no correction of what " +
	"looks like a mistake, and above all no figure that is not printed. If part of the page is " +
	"illegible, write [illegible] there rather than guessing at it."

const userPrompt = "Transcribe this page. Keep the reading order and the line breaks. " +
	"Render a table as a markdown pipe table with the same rows and columns as the page. " +
	"Do not add a heading, a caption or any commentary of your own."
