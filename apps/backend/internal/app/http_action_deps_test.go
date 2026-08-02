package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/adapters/mcp"
)

// These exercise the real egress guard, because the SSRF property is the whole
// point of the ticket and a fake would prove nothing. AllowPrivate is on only
// where a test needs to reach httptest's 127.0.0.1 — the same escape hatch the
// gate uses for a local endpoint.

func TestGuardEgressReachesAllowedHostWithHeaders(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Test")
		w.WriteHeader(202)
		_, _ = w.Write([]byte("ok-body"))
	}))
	defer srv.Close()

	eg := NewHTTPActionEgress(mcp.Guard{AllowPrivate: true, Timeout: 5 * time.Second}, 0)
	status, body, err := eg.Do(context.Background(), "GET", srv.URL, map[string]string{"X-Test": "v"}, nil)
	if err != nil {
		t.Fatalf("Do = %v; want nil against an allowed host", err)
	}
	if status != 202 || string(body) != "ok-body" {
		t.Fatalf("got %d %q; want 202 ok-body", status, body)
	}
	if gotHeader != "v" {
		t.Fatalf("server saw X-Test=%q; want the header the caller set", gotHeader)
	}
}

func TestGuardEgressBlocksMetadataEndpoint(t *testing.T) {
	// The address this whole guard exists for. http is allowed here only so the
	// address rule, not the scheme rule, is what refuses it.
	eg := NewHTTPActionEgress(mcp.Guard{AllowInsecureHTTP: true, Timeout: 2 * time.Second}, 0)
	_, _, err := eg.Do(context.Background(), "GET", "http://169.254.169.254/latest/meta-data/", nil, nil)
	if err == nil {
		t.Fatal("Do(169.254.169.254) = nil; want the guard to refuse the metadata endpoint")
	}
	if !strings.Contains(err.Error(), "link-local") {
		t.Fatalf("err = %v; want a link-local refusal", err)
	}
}

func TestGuardEgressBlocksLoopbackWhenPrivateDisallowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// The default guard: no private egress. httptest lives on 127.0.0.1, so this is
	// our own network and the call must not complete.
	eg := NewHTTPActionEgress(mcp.Guard{AllowInsecureHTTP: true, Timeout: 2 * time.Second}, 0)
	if _, _, err := eg.Do(context.Background(), "GET", srv.URL, nil, nil); err == nil {
		t.Fatal("Do(127.0.0.1, private disallowed) = nil; want a refusal")
	}
}

func TestGuardEgressRefusesRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "https://example.com/elsewhere", http.StatusFound)
	}))
	defer srv.Close()

	eg := NewHTTPActionEgress(mcp.Guard{AllowPrivate: true, Timeout: 5 * time.Second}, 0)
	// A registered endpoint has one fixed host; a 3xx is a call trying to leave it,
	// and StrictClient refuses to follow it rather than re-validating a hop.
	if _, _, err := eg.Do(context.Background(), "GET", srv.URL, nil, nil); err == nil {
		t.Fatal("Do(redirect) = nil; want http_action to refuse to follow it")
	}
}

func TestGuardEgressCapsResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(strings.Repeat("x", 1000)))
	}))
	defer srv.Close()

	eg := NewHTTPActionEgress(mcp.Guard{AllowPrivate: true, Timeout: 5 * time.Second}, 16)
	_, body, err := eg.Do(context.Background(), "GET", srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("Do = %v; want nil", err)
	}
	if len(body) != 16 {
		t.Fatalf("body length = %d; want it capped at 16", len(body))
	}
}
