package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

// DefaultBaseURL is Slack's Web API root. Overridable via SLACK_API_BASE_URL
// so tests (and any proxy deployments) can point elsewhere.
const DefaultBaseURL = "https://slack.com/api"

// tokenTTL bounds how long a decrypted bot token is cached before it is
// re-read from the control plane. Slack bot tokens do not expire on their
// own, so this only bounds staleness after a rotation; auth failures evict
// immediately regardless.
const tokenTTL = 30 * time.Minute

// Client is a per-process Slack Web API client. It caches one decrypted
// bot token per company_id; on an auth error it evicts the entry, re-reads
// the credential, and retries once. Unlike Lark there is no token exchange
// — the xoxb- token is used directly.
type Client struct {
	creds      domain.CompanySlackCredentialRepository
	cipher     *crypto.DSNCipher
	baseURL    string
	httpClient *http.Client

	mu     sync.Mutex
	tokens map[string]tokenEntry
}

type tokenEntry struct {
	token     string
	expiresAt time.Time
}

// NewClient wires a Client. baseURL is optional; defaults to DefaultBaseURL.
func NewClient(creds domain.CompanySlackCredentialRepository, cipher *crypto.DSNCipher, baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		creds:      creds,
		cipher:     cipher,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 20 * time.Second},
		tokens:     map[string]tokenEntry{},
	}
}

// Reply posts an in-thread message to channelID. threadTS is the thread the
// reply belongs to — for the first answer to a top-level @mention that is
// the mention's own ts, which makes Slack start a thread under it.
func (c *Client) Reply(ctx context.Context, companyID, channelID, threadTS, content string) error {
	if companyID == "" || channelID == "" {
		return errors.New("slack: company_id and channel_id required")
	}
	body, err := json.Marshal(postMessageBody{
		Channel:  channelID,
		Text:     ToMrkdwn(content),
		ThreadTS: threadTS,
	})
	if err != nil {
		return fmt.Errorf("marshal chat.postMessage body: %w", err)
	}
	return c.do(ctx, companyID, c.baseURL+"/chat.postMessage", body)
}

// Send posts a top-level message to channelID. This is proactive delivery —
// a watcher breach, not an answer to anything — so it carries no thread_ts
// and starts its own thread in the channel.
func (c *Client) Send(ctx context.Context, companyID, channelID, content string) error {
	return c.Reply(ctx, companyID, channelID, "", content)
}

type postMessageBody struct {
	Channel  string `json:"channel"`
	Text     string `json:"text"`
	ThreadTS string `json:"thread_ts,omitempty"`
}

// authErrors are the Slack error codes that mean "this token is no longer
// usable" — the only class worth evicting the cache and retrying for.
var authErrors = map[string]bool{
	"invalid_auth":     true,
	"token_revoked":    true,
	"token_expired":    true,
	"account_inactive": true,
	"not_authed":       true,
}

// do executes one authenticated Web API call, refreshing the cached token
// once when Slack reports an auth failure.
func (c *Client) do(ctx context.Context, companyID, url string, body []byte) error {
	token, err := c.tokenFor(ctx, companyID, false)
	if err != nil {
		return err
	}
	apiErr, err := c.send(ctx, url, token, body)
	if err != nil {
		return err
	}
	if apiErr != "" && authErrors[apiErr] {
		c.EvictToken(companyID)
		token, err = c.tokenFor(ctx, companyID, true)
		if err != nil {
			return err
		}
		apiErr, err = c.send(ctx, url, token, body)
		if err != nil {
			return err
		}
	}
	if apiErr != "" {
		return fmt.Errorf("slack api %s: %s", url, apiErr)
	}
	return nil
}

// send performs the HTTP call and returns Slack's logical error code, if
// any. Slack answers 200 with `{"ok":false,"error":"..."}` for most
// failures, so a non-empty first return value is the normal failure path.
func (c *Client) send(ctx context.Context, url, token string, body []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("slack request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("slack api %s: status=%d body=%s", url, resp.StatusCode, string(raw))
	}
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out.OK {
		return "", nil
	}
	if out.Error == "" {
		out.Error = "unknown_error"
	}
	return out.Error, nil
}

// tokenFor returns the cached bot token or decrypts a fresh one from the
// control plane. forceRefresh bypasses the cache (used after an auth error).
func (c *Client) tokenFor(ctx context.Context, companyID string, forceRefresh bool) (string, error) {
	if !forceRefresh {
		c.mu.Lock()
		ent, ok := c.tokens[companyID]
		c.mu.Unlock()
		if ok && time.Now().Before(ent.expiresAt) {
			return ent.token, nil
		}
	}

	cred, err := c.creds.Get(ctx, companyID)
	if err != nil {
		return "", fmt.Errorf("load slack credential: %w", err)
	}
	if !cred.Enabled {
		return "", fmt.Errorf("slack disabled for company %s", companyID)
	}
	token, err := c.cipher.Decrypt(cred.BotTokenEncrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt bot_token: %w", err)
	}

	c.mu.Lock()
	c.tokens[companyID] = tokenEntry{token: token, expiresAt: time.Now().Add(tokenTTL)}
	c.mu.Unlock()
	return token, nil
}

// EvictToken drops the cached token for a company, e.g. after credential
// rotation. Safe to call concurrently.
func (c *Client) EvictToken(companyID string) {
	c.mu.Lock()
	delete(c.tokens, companyID)
	c.mu.Unlock()
}

var _ Provider = (*Client)(nil)
