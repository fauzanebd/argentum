package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
)

// The dashboard's half of T-A5: what a key has been doing, beside the key.
//
// The handler's own decisions are what these cover — that a failed counters read
// degrades to a list instead of an error, that a deployment without the recorder
// answers an empty list rather than a 503, and that the key filter reaches the
// repository. The admin-only half is `cmd/api`'s policy table and its own test.

type fakeTraffic struct {
	stats     map[string]*domain.APIKeyRequestStats
	statsErr  error
	errs      []*domain.APIRequestError
	errsErr   error
	since     time.Time
	askedKey  string
	askedLim  int
	statCalls int
}

func (f *fakeTraffic) StatsByKey(
	_ context.Context, _ string, since time.Time,
) (map[string]*domain.APIKeyRequestStats, error) {
	f.since = since
	f.statCalls++
	return f.stats, f.statsErr
}

func (f *fakeTraffic) RecentErrors(
	_ context.Context, _, keyID string, limit int,
) ([]*domain.APIRequestError, error) {
	f.askedKey, f.askedLim = keyID, limit
	return f.errs, f.errsErr
}

func keysRouter(traffic APIKeyTrafficReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api")
	g.Use(func(c *gin.Context) {
		c.Set("company_id", testCompany)
		c.Set("user_id", "user-1")
		c.Next()
	})
	// svc is nil: `GET /api/api-keys` needs it, so those cases build their own
	// router. What is under test here is the traffic half, which is reachable
	// with the errors route alone.
	NewAPIKeysHandler(nil).WithTraffic(traffic).Register(g)
	return r
}

func getJSON(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestErrorListCarriesWhatAnIntegratorNeeds: the request id they were handed,
// the route, the status, and the code they can match on in their own code.
func TestErrorListCarriesWhatAnIntegratorNeeds(t *testing.T) {
	traffic := &fakeTraffic{errs: []*domain.APIRequestError{{
		ID: "e-1", APIKeyID: "key-1", RequestID: "req_abc123",
		Method: "POST", Route: "/v1/reports", Status: 403,
		ErrorCode: "insufficient_scope", ErrorType: "permission",
		LatencyMS: 3, CreatedAt: time.Now().UTC(),
	}}}

	w := getJSON(t, keysRouter(traffic), "/api/api-keys/errors")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body APIKeyErrorsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Limit != errorListLimit {
		t.Errorf("limit = %d, want %d", body.Limit, errorListLimit)
	}
	if len(body.Errors) != 1 {
		t.Fatalf("returned %d rows", len(body.Errors))
	}
	got := body.Errors[0]
	if got.RequestID != "req_abc123" || got.ErrorCode != "insufficient_scope" || got.Status != 403 {
		t.Errorf("row = %+v", got)
	}
	if traffic.askedLim != errorListLimit {
		t.Errorf("asked the repository for %d rows, want %d", traffic.askedLim, errorListLimit)
	}
	// The company id is never echoed: every read here is already company-scoped,
	// and repeating the tenant's own id in fifty rows tells the reader nothing.
	var raw []map[string]any
	if err := json.Unmarshal(mustField(t, w.Body.Bytes(), "errors"), &raw); err != nil {
		t.Fatalf("decode rows: %v", err)
	}
	if _, present := raw[0]["company_id"]; present {
		t.Error("the row carries company_id on the wire")
	}
}

// TestErrorListFiltersByKey — the panel is opened for one key at a time.
func TestErrorListFiltersByKey(t *testing.T) {
	traffic := &fakeTraffic{}
	getJSON(t, keysRouter(traffic), "/api/api-keys/errors?key_id=key-9")
	if traffic.askedKey != "key-9" {
		t.Errorf("filtered on %q, want key-9", traffic.askedKey)
	}
}

// TestErrorListWithoutTheRecorderIsEmptyNotBroken. A 503 would put an error
// banner on a page whose primary job — managing keys — works fine.
func TestErrorListWithoutTheRecorderIsEmptyNotBroken(t *testing.T) {
	w := getJSON(t, keysRouter(nil), "/api/api-keys/errors")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body APIKeyErrorsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Errors == nil {
		t.Error("errors is null; a client iterating the list should not need a null check")
	}
	if len(body.Errors) != 0 || body.Limit != errorListLimit {
		t.Errorf("body = %+v", body)
	}
}

// TestErrorListReportsAFailedRead. Distinct from the case above: the feature is
// present and broken, which the tab should say rather than show as "no errors".
func TestErrorListReportsAFailedRead(t *testing.T) {
	traffic := &fakeTraffic{errsErr: errors.New("relation does not exist")}
	if w := getJSON(t, keysRouter(traffic), "/api/api-keys/errors"); w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// TestStatsWindowIsAWholeNumberOfHours: the rollup is bucketed by hour, and a
// `since` in the middle of one would drop the current hour — the only hour
// anyone debugging is looking at.
func TestStatsWindowIsAWholeNumberOfHours(t *testing.T) {
	traffic := &fakeTraffic{stats: map[string]*domain.APIKeyRequestStats{
		"key-1": {APIKeyID: "key-1", Requests: 10, Failed: 2, ErrorRatePct: 20},
	}}
	h := NewAPIKeysHandler(nil).WithTraffic(traffic)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/api-keys", nil)
	c.Set("company_id", testCompany)

	stats := h.statsFor(c)
	if traffic.since.Truncate(time.Hour) != traffic.since {
		t.Errorf("since = %s, want it truncated to the hour", traffic.since)
	}
	if age := time.Since(traffic.since); age < statsWindow || age > statsWindow+time.Hour {
		t.Errorf("window is %s wide, want ~%s", age, statsWindow)
	}
	if stats["key-1"].WindowHours != 24 {
		t.Errorf("window_hours = %d, want 24 — a count with no period is unreadable", stats["key-1"].WindowHours)
	}
}

// TestStatsFailureDoesNotBlockTheRoster. An admin who needs to revoke a leaked
// credential must not be stopped by an unreadable counters table.
func TestStatsFailureDoesNotBlockTheRoster(t *testing.T) {
	traffic := &fakeTraffic{statsErr: errors.New("connection refused")}
	h := NewAPIKeysHandler(nil).WithTraffic(traffic)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/api-keys", nil)
	c.Set("company_id", testCompany)

	if got := h.statsFor(c); got != nil {
		t.Errorf("stats = %v, want nil so the list still renders", got)
	}
	if traffic.statCalls != 1 {
		t.Errorf("statsFor called the repository %d times", traffic.statCalls)
	}
}

// mustField pulls one raw field out of a JSON object.
func mustField(t *testing.T, body []byte, field string) []byte {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	raw, ok := envelope[field]
	if !ok {
		t.Fatalf("no %q in %s", field, body)
	}
	return raw
}
