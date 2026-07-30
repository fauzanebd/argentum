package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/metrics"
)

// `/metrics` and the per-key labels T-A5 adds to it.
//
// The endpoint has no credential of its own — T-17 is the ticket for moving it
// off the public router — and the labels being added name a tenant's own key
// ids. So the property under test is narrow and load-bearing: those labels go
// out to a caller with the token, and to nobody else.

func metricsRequest(t *testing.T, r *gin.Engine, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "/metrics", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
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
		t.Error("no route block; route numbers name no tenant and should always be served")
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

func TestMetricsKeyLabelsNeedTheToken(t *testing.T) {
	r := metricsRouter(t, "s3cret")

	if keys := keyLabels(t, metricsRequest(t, r, "s3cret")); len(keys) != 1 {
		t.Errorf("an authorized scrape got %d key labels, want 1", len(keys))
	}
	if keys := keyLabels(t, metricsRequest(t, r, "")); len(keys) != 0 {
		t.Errorf("an unauthenticated scrape got key labels: %v", keys)
	}
	if keys := keyLabels(t, metricsRequest(t, r, "wrong")); len(keys) != 0 {
		t.Errorf("a wrong token got key labels: %v", keys)
	}
}

// TestUnsetTokenIsNeverAMatch is the failure mode worth a test of its own:
// leaving the setting empty must not turn every scrape into an authorized one.
func TestUnsetTokenIsNeverAMatch(t *testing.T) {
	r := metricsRouter(t, "")

	for _, bearer := range []string{"", "anything"} {
		if keys := keyLabels(t, metricsRequest(t, r, bearer)); len(keys) != 0 {
			t.Errorf("bearer %q got key labels while METRICS_TOKEN is unset: %v", bearer, keys)
		}
	}
}
