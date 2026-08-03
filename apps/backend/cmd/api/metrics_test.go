package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/metrics"
)

// `/metrics`: who may read it at all, and who may read the per-key labels.
//
// Two properties, and the first one is the ticket (T-17's first bullet). The
// snapshot carries this deployment's LLM spend, token totals and query volumes,
// so an endpoint served to anyone who could reach the pod was publishing cost
// data. It now answers to the token, or — with no token configured — to
// loopback only. The second property is T-A5's: the per-key block names a
// tenant's own key ids and goes out to a credentialed scrape and to nobody else.

const (
	remotePeer   = "203.0.113.9:51234"
	loopbackPeer = "127.0.0.1:51234"
)

// metricsRequest asks for the JSON snapshot. Since T-17 the endpoint's default
// is Prometheus exposition, and these cases are about *who may read it* rather
// than about the format — so they keep asserting against the shape that has
// fields to assert on. The default is covered by
// TestMetricsServesExpositionByDefault below.
func metricsRequest(t *testing.T, r *gin.Engine, bearer, peer string) *httptest.ResponseRecorder {
	t.Helper()
	return metricsRequestFormat(t, r, bearer, peer, "?format=json")
}

func metricsRequestFormat(t *testing.T, r *gin.Engine, bearer, peer, query string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "/metrics"+query, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.RemoteAddr = peer
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// T-17: a scraper asks for nothing in particular and gets the exposition.
func TestMetricsServesExpositionByDefault(t *testing.T) {
	r := metricsRouter(t, "s3cret")

	w := metricsRequestFormat(t, r, "s3cret", remotePeer, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want the exposition's text/plain", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "# TYPE argentum_v1_requests_total counter") {
		t.Errorf("body is not exposition format:\n%s", body)
	}
	if !strings.Contains(body, `argentum_v1_key_requests_total{key_id="key-belonging-to-a-tenant"}`) {
		t.Error("an authorized scrape did not get the per-key series")
	}
}

// And the same endpoint without the credential renders no key series — the
// stripping is in the snapshot, so it holds for both formats.
func TestExpositionStripsKeyLabelsWithoutTheToken(t *testing.T) {
	r := metricsRouter(t, "")

	w := metricsRequestFormat(t, r, "", loopbackPeer, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "key-belonging-to-a-tenant") {
		t.Errorf("an unauthenticated exposition carried a key id:\n%s", w.Body.String())
	}
}

// An explicit Accept still gets JSON; a browser's `*/*` does not.
func TestAcceptJSONGetsTheSnapshot(t *testing.T) {
	r := metricsRouter(t, "s3cret")

	req, err := http.NewRequest(http.MethodGet, "/metrics", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer s3cret")
	req.Header.Set("Accept", "application/json")
	req.RemoteAddr = remotePeer
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
}

// keyLabels reads the per-key block out of a /metrics response.
func keyLabels(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		APIV1 struct {
			Routes map[string]any `json:"routes"`
			Keys   map[string]any `json:"keys"`
		} `json:"api_v1"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if len(body.APIV1.Routes) == 0 {
		t.Error("no route block; a scrape that is served at all gets the route numbers")
	}
	return body.APIV1.Keys
}

// metricsRouter builds the real router with one recorded request already in the
// collector, so there is something to strip.
func metricsRouter(t *testing.T, token string) *gin.Engine {
	t.Helper()
	metrics.Default().Reset()
	metrics.Default().RecordAPIRequest("GET", "/v1/me", "key-belonging-to-a-tenant", 200, 0)
	return routerWith(t, func(c *config.Config) { c.MetricsToken = token })
}

// The finding, closed: a remote caller with no credential does not read this
// deployment's spend. 404 rather than 401 — an endpoint with no way to
// authenticate anybody should not advertise that it exists.
func TestMetricsIsNotServedToARemoteCallerWithoutAToken(t *testing.T) {
	r := metricsRouter(t, "")

	for _, bearer := range []string{"", "anything"} {
		w := metricsRequest(t, r, bearer, remotePeer)
		if w.Code != http.StatusNotFound {
			t.Errorf("bearer %q from a remote peer got %d, want 404 (body: %s)", bearer, w.Code, w.Body.String())
		}
	}
}

// A token that is set is the only way in, and a wrong one is not a downgrade to
// the public view — it is a refusal.
func TestMetricsRefusesAWrongTokenOutright(t *testing.T) {
	r := metricsRouter(t, "s3cret")

	for _, bearer := range []string{"", "wrong"} {
		w := metricsRequest(t, r, bearer, remotePeer)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("bearer %q got %d, want 401 (body: %s)", bearer, w.Code, w.Body.String())
		}
	}
}

// Loopback with the token set is still not a way in: the peer address is a
// convenience for a deployment that configured nothing, not a second credential
// that outranks the one that was configured.
func TestASetTokenIsRequiredEvenOnLoopback(t *testing.T) {
	r := metricsRouter(t, "s3cret")

	if w := metricsRequest(t, r, "", loopbackPeer); w.Code != http.StatusUnauthorized {
		t.Errorf("loopback with no token got %d, want 401", w.Code)
	}
}

func TestMetricsKeyLabelsNeedTheToken(t *testing.T) {
	r := metricsRouter(t, "s3cret")

	if keys := keyLabels(t, metricsRequest(t, r, "s3cret", remotePeer)); len(keys) != 1 {
		t.Errorf("an authorized scrape got %d key labels, want 1", len(keys))
	}
}

// TestUnsetTokenIsNeverAMatch is the failure mode worth a test of its own:
// leaving the setting empty must not turn every scrape into an authorized one.
// With no token the loopback caller is served, and still without the labels.
func TestUnsetTokenIsNeverAMatch(t *testing.T) {
	r := metricsRouter(t, "")

	for _, bearer := range []string{"", "anything"} {
		if keys := keyLabels(t, metricsRequest(t, r, bearer, loopbackPeer)); len(keys) != 0 {
			t.Errorf("bearer %q got key labels while METRICS_TOKEN is unset: %v", bearer, keys)
		}
	}
}

// The peer address is read off the socket, never out of a header — gin's
// ClientIP() resolves X-Forwarded-For by default, and this decides whether cost
// data is served.
func TestForwardedHeadersDoNotMakeACallerLocal(t *testing.T) {
	r := metricsRouter(t, "")

	for _, header := range []string{"X-Forwarded-For", "X-Real-Ip"} {
		req, err := http.NewRequest(http.MethodGet, "/metrics", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.RemoteAddr = remotePeer
		req.Header.Set(header, "127.0.0.1")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: spoofed loopback got %d, want 404", header, w.Code)
		}
	}
}

func TestFromLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:51234": true,
		"[::1]:51234":     true,
		"127.0.0.1":       true,
		"::1":             true,
		"203.0.113.9:443": false,
		"10.1.2.3:8080":   false,
		"":                false,
		"not-an-address":  false,
	}
	for addr, want := range cases {
		if got := fromLoopback(addr); got != want {
			t.Errorf("fromLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}
