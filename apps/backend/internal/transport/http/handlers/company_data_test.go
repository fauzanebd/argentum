package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// `DELETE /api/company/data` has no undo, no soft-delete and no recycle bin.
// The confirmation is the only thing between a mistyped `curl` and a tenant's
// whole conversation history, so it is tested from the outside — through a real
// router, with a real body — rather than by calling the handler method.

type stubRetentionRepo struct {
	erased   []string
	exported []domain.ExportedMessage
}

func (s *stubRetentionRepo) PurgeCompanyMessages(context.Context, string, time.Time) (int, int, error) {
	return 0, 0, nil
}
func (s *stubRetentionRepo) HasExpired(context.Context, string, time.Time) (bool, error) {
	return false, nil
}
func (s *stubRetentionRepo) EraseCompanyConversations(_ context.Context, companyID string) (int, int, error) {
	s.erased = append(s.erased, companyID)
	return 2, 7, nil
}
func (s *stubRetentionRepo) CompaniesWithRetention(context.Context) ([]domain.CompanyRetention, error) {
	return nil, nil
}
func (s *stubRetentionRepo) ExportCompanyConversations(_ context.Context, _ string, fn func(domain.ExportedMessage) error) error {
	for _, m := range s.exported {
		if err := fn(m); err != nil {
			return err
		}
	}
	return nil
}

type stubErasureRecords struct{ n int }

func (s *stubErasureRecords) Begin(_ context.Context, e *domain.DataErasure) error {
	s.n++
	e.ID = "erasure-1"
	return nil
}
func (s *stubErasureRecords) Complete(context.Context, string, int, int) error { return nil }
func (s *stubErasureRecords) Fail(context.Context, string, string) error       { return nil }
func (s *stubErasureRecords) ListByCompany(context.Context, string, int) ([]*domain.DataErasure, error) {
	return []*domain.DataErasure{{ID: "erasure-1", Scope: domain.ErasureScopeAll}}, nil
}

// dataRouter mounts the handler behind a middleware that sets the identity Auth
// would set, which is the only thing the handler reads off the context.
func dataRouter(repo domain.RetentionRepository, records domain.DataErasureRepository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api")
	g.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "user-9")
		c.Next()
	})
	NewCompanyDataHandler(app.NewRetentionService(repo, records, nil)).Register(g)
	return r
}

func TestEraseRefusesWithoutTheCompanyIdEchoedBack(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"confirm_company_id":""}`,
		`{"confirm_company_id":"co-2"}`,
		`{"confirm_company_id":"CO-1"}`,
	} {
		repo := &stubRetentionRepo{}
		r := dataRouter(repo, &stubErasureRecords{})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/api/company/data", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("body %s → %d, want 400", body, w.Code)
		}
		if len(repo.erased) != 0 {
			t.Errorf("body %s erased %v anyway", body, repo.erased)
		}
	}
}

func TestEraseDeletesAndReturnsTheRecord(t *testing.T) {
	repo := &stubRetentionRepo{}
	records := &stubErasureRecords{}
	r := dataRouter(repo, records)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/company/data",
		strings.NewReader(`{"confirm_company_id":"co-1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if len(repo.erased) != 1 || repo.erased[0] != "co-1" {
		t.Errorf("erased %v, want [co-1]", repo.erased)
	}

	// The record is the deliverable, not a side effect: a tenant discharging
	// their own erasure obligation needs something to file.
	var rec domain.DataErasure
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
		t.Fatalf("response is not an erasure record: %v (%s)", err, w.Body.String())
	}
	if rec.Status != domain.ErasureStatusCompleted {
		t.Errorf("status = %q, want %q", rec.Status, domain.ErasureStatusCompleted)
	}
	if rec.ThreadsDeleted != 2 || rec.MessagesDeleted != 7 {
		t.Errorf("counts = %d / %d, want 2 / 7", rec.ThreadsDeleted, rec.MessagesDeleted)
	}
	if rec.RequestedBy != "user-9" {
		t.Errorf("requested_by = %q, want the authenticated user", rec.RequestedBy)
	}
}

// One JSON object per line, not one array. A tenant large enough to need the
// export is a tenant whose history should never be buffered whole to serialise
// it — and the shape is what makes that possible, so it is asserted rather than
// left to the implementation.
func TestExportIsNewlineDelimitedJSON(t *testing.T) {
	repo := &stubRetentionRepo{exported: []domain.ExportedMessage{
		{ThreadID: "t1", MessageID: "m1", Role: "user", Content: "berapa penjualan bulan lalu?"},
		{ThreadID: "t1", MessageID: "m2", Role: "assistant", Content: "Rp 3,86 Miliar"},
	}}
	r := dataRouter(repo, &stubErasureRecords{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/company/data/export", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q, want application/x-ndjson", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	}

	var lines int
	sc := bufio.NewScanner(strings.NewReader(w.Body.String()))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var m domain.ExportedMessage
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line %d is not a JSON object: %v (%s)", lines+1, err, sc.Text())
		}
		lines++
	}
	if lines != 2 {
		t.Errorf("exported %d lines, want 2", lines)
	}
}

func TestHistoryServesTheWrittenRecord(t *testing.T) {
	r := dataRouter(&stubRetentionRepo{}, &stubErasureRecords{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/company/data/erasures", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Erasures []domain.DataErasure `json:"erasures"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	if len(body.Erasures) != 1 {
		t.Errorf("got %d records, want 1", len(body.Erasures))
	}
}
