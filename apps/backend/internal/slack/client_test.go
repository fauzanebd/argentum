package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

// 32 bytes of key material for the AES-256-GCM cipher.
const testKeyHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

// fakeCredRepo serves one credential row and counts reads so tests can
// assert the token cache is actually used (and evicted).
type fakeCredRepo struct {
	mu   sync.Mutex
	row  *domain.CompanySlackCredential
	gets int
}

func (f *fakeCredRepo) Get(_ context.Context, _ string) (*domain.CompanySlackCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.row == nil {
		return nil, domain.ErrNotFound
	}
	cp := *f.row
	return &cp, nil
}

func (f *fakeCredRepo) getCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

func (f *fakeCredRepo) GetByAppID(context.Context, string) (*domain.CompanySlackCredential, error) {
	return nil, domain.ErrNotFound
}
func (f *fakeCredRepo) Upsert(context.Context, *domain.CompanySlackCredential) error { return nil }
func (f *fakeCredRepo) Delete(context.Context, string) error                         { return nil }
func (f *fakeCredRepo) ListEnabled(context.Context) ([]*domain.CompanySlackCredential, error) {
	return nil, nil
}

func newTestClient(t *testing.T, handler http.HandlerFunc, enabled bool) (*Client, *fakeCredRepo, *httptest.Server) {
	t.Helper()
	cipher, err := crypto.NewFromHex(testKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := cipher.Encrypt("xoxb-test-token")
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeCredRepo{row: &domain.CompanySlackCredential{
		CompanyID:         "co-1",
		AppID:             "A123",
		BotTokenEncrypted: enc,
		SigningSecret:     testSecret,
		Enabled:           enabled,
	}}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(repo, cipher, srv.URL), repo, srv
}

func TestClient_Reply_postsThreadedMessage(t *testing.T) {
	var got postMessageBody
	var authHeader string
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat.postMessage") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		authHeader = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.Write([]byte(`{"ok":true}`))
	}, true)

	err := c.Reply(context.Background(), "co-1", "C123", "1700000000.000100",
		"See [the dashboard](https://mb.example/d/1)")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if authHeader != "Bearer xoxb-test-token" {
		t.Fatalf("auth header: %q", authHeader)
	}
	if got.Channel != "C123" || got.ThreadTS != "1700000000.000100" {
		t.Fatalf("post body: %+v", got)
	}
	// The agent's Markdown must reach Slack as mrkdwn.
	if got.Text != "See <https://mb.example/d/1|the dashboard>" {
		t.Fatalf("text not converted: %q", got.Text)
	}
}

func TestClient_Reply_cachesToken(t *testing.T) {
	c, repo, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}, true)

	for i := 0; i < 3; i++ {
		if err := c.Reply(context.Background(), "co-1", "C123", "", "hi"); err != nil {
			t.Fatalf("Reply %d: %v", i, err)
		}
	}
	if n := repo.getCount(); n != 1 {
		t.Fatalf("credential reads = %d, want 1 (token should be cached)", n)
	}
}

func TestClient_Reply_retriesOnceAfterAuthError(t *testing.T) {
	var calls int
	c, repo, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}, true)

	if err := c.Reply(context.Background(), "co-1", "C123", "", "hi"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if calls != 2 {
		t.Fatalf("HTTP calls = %d, want 2 (one retry)", calls)
	}
	if n := repo.getCount(); n != 2 {
		t.Fatalf("credential reads = %d, want 2 (cache evicted then re-read)", n)
	}
}

func TestClient_Reply_nonAuthErrorNotRetried(t *testing.T) {
	var calls int
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
	}, true)

	err := c.Reply(context.Background(), "co-1", "C123", "", "hi")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "channel_not_found") {
		t.Fatalf("error should surface Slack's code: %v", err)
	}
	if calls != 1 {
		t.Fatalf("HTTP calls = %d, want 1 (no retry on a non-auth error)", calls)
	}
}

func TestClient_Reply_disabledTenant(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("HTTP call made for a disabled tenant")
	}, false)

	if err := c.Reply(context.Background(), "co-1", "C123", "", "hi"); err == nil {
		t.Fatal("expected error for disabled tenant")
	}
}

func TestClient_Reply_requiresIDs(t *testing.T) {
	c, _, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("HTTP call made without required ids")
	}, true)

	if err := c.Reply(context.Background(), "", "C123", "", "hi"); err == nil {
		t.Fatal("expected error for empty company_id")
	}
	if err := c.Reply(context.Background(), "co-1", "", "", "hi"); err == nil {
		t.Fatal("expected error for empty channel_id")
	}
}
