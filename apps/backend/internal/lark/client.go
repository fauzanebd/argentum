package lark

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

// DefaultBaseURL is Lark's global endpoint. Feishu (China) deployments must
// override via the LARK_API_BASE_URL env var.
const DefaultBaseURL = "https://open.larksuite.com"

// Client is a per-process Lark Open Platform REST client. It caches one
// tenant_access_token per company_id; on 401 it evicts the entry and
// retries once. Credentials and the AES-GCM secret cipher are injected so
// the worker can decrypt app_secret without any extra ceremony.
type Client struct {
	creds      domain.CompanyLarkCredentialRepository
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
func NewClient(creds domain.CompanyLarkCredentialRepository, cipher *crypto.DSNCipher, baseURL string) *Client {
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

// Reply posts an in-thread reply to messageID. Lark's reply endpoint
// preserves the parent's thread; passing reply_in_thread:true forces a new
// thread to be created when the parent isn't yet in one (the first
// @mention from a main chat).
func (c *Client) Reply(ctx context.Context, companyID, messageID, content string) error {
	if companyID == "" || messageID == "" {
		return errors.New("lark: company_id and message_id required")
	}
	body, err := buildTextReply(content)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("%s/open-apis/im/v1/messages/%s/reply", c.baseURL, messageID)
	if err := c.do(ctx, companyID, http.MethodPost, endpoint, body); err != nil {
		return err
	}
	return nil
}

// Send posts a new message into chatID, not a reply to an existing one (T-08).
// A watcher fires proactively — there is no parent message — so it uses the
// create endpoint with receive_id_type=chat_id rather than the reply endpoint.
func (c *Client) Send(ctx context.Context, companyID, chatID, content string) error {
	if companyID == "" || chatID == "" {
		return errors.New("lark: company_id and chat_id required")
	}
	inner, err := json.Marshal(TextContent{Text: content})
	if err != nil {
		return fmt.Errorf("marshal text content: %w", err)
	}
	body, err := json.Marshal(sendBody{
		ReceiveID: chatID,
		MsgType:   "text",
		Content:   string(inner),
	})
	if err != nil {
		return fmt.Errorf("marshal send body: %w", err)
	}
	endpoint := fmt.Sprintf("%s/open-apis/im/v1/messages?receive_id_type=chat_id", c.baseURL)
	return c.do(ctx, companyID, http.MethodPost, endpoint, body)
}

type replyBody struct {
	MsgType       string `json:"msg_type"`
	Content       string `json:"content"`
	ReplyInThread bool   `json:"reply_in_thread"`
}

type sendBody struct {
	ReceiveID string `json:"receive_id"`
	MsgType   string `json:"msg_type"`
	Content   string `json:"content"`
}

func buildTextReply(content string) ([]byte, error) {
	inner, err := json.Marshal(TextContent{Text: content})
	if err != nil {
		return nil, fmt.Errorf("marshal text content: %w", err)
	}
	return json.Marshal(replyBody{
		MsgType:       "text",
		Content:       string(inner),
		ReplyInThread: true,
	})
}

// do executes one authenticated request, refreshing the token once on 401.
func (c *Client) do(ctx context.Context, companyID, method, url string, body []byte) error {
	token, err := c.tokenFor(ctx, companyID, false)
	if err != nil {
		return err
	}
	resp, err := c.send(ctx, method, url, token, body)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		token, err = c.tokenFor(ctx, companyID, true)
		if err != nil {
			return err
		}
		resp, err = c.send(ctx, method, url, token, body)
		if err != nil {
			return err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("lark api %s %s: status=%d body=%s", method, url, resp.StatusCode, string(b))
	}
	// Lark returns 200 even on logical failure; surface non-zero `code`.
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &env)
	}
	if env.Code != 0 {
		return fmt.Errorf("lark api %s: code=%d msg=%s", url, env.Code, env.Msg)
	}
	return nil
}

func (c *Client) send(ctx context.Context, method, url, token string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	return c.httpClient.Do(req)
}

// tokenFor returns a cached tenant_access_token or refreshes it. forceRefresh
// bypasses the cache (used after a 401).
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
		return "", fmt.Errorf("load lark credential: %w", err)
	}
	if !cred.Enabled {
		return "", fmt.Errorf("lark disabled for company %s", companyID)
	}
	appSecret, err := c.cipher.Decrypt(cred.AppSecretEncrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt app_secret: %w", err)
	}

	token, expiresIn, err := c.fetchToken(ctx, cred.AppID, appSecret)
	if err != nil {
		return "", err
	}
	// 5-minute safety margin to avoid using a token that's about to expire.
	expiry := time.Now().Add(time.Duration(expiresIn)*time.Second - 5*time.Minute)
	c.mu.Lock()
	c.tokens[companyID] = tokenEntry{token: token, expiresAt: expiry}
	c.mu.Unlock()
	return token, nil
}

func (c *Client) fetchToken(ctx context.Context, appID, appSecret string) (string, int, error) {
	url := c.baseURL + "/open-apis/auth/v3/tenant_access_token/internal"
	reqBody, err := json.Marshal(map[string]string{"app_id": appID, "app_secret": appSecret})
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("tenant_access_token request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", 0, fmt.Errorf("tenant_access_token status=%d body=%s", resp.StatusCode, string(raw))
	}
	var out struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", 0, fmt.Errorf("decode tenant_access_token: %w", err)
	}
	if out.Code != 0 || out.TenantAccessToken == "" {
		return "", 0, fmt.Errorf("tenant_access_token code=%d msg=%s", out.Code, out.Msg)
	}
	if out.Expire <= 0 {
		out.Expire = 7200
	}
	return out.TenantAccessToken, out.Expire, nil
}

// EvictToken drops the cached token for a company, e.g. after credential
// rotation. Safe to call concurrently.
func (c *Client) EvictToken(companyID string) {
	c.mu.Lock()
	delete(c.tokens, companyID)
	c.mu.Unlock()
}

var _ Provider = (*Client)(nil)
