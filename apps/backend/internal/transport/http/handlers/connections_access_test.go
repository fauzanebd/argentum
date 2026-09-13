package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z6 on sources: a member's source list is narrowed by grant; an admin's is
// the whole list, because it is where sources are configured. The three routes
// that read what is in a source are resourcePolicy's, proven in cmd/api.

// listedSources is a company's sources. It embeds the interface, so the list
// route reaching anything but ListByCompany panics.
type listedSources struct {
	domain.ConnectionRepository
	rows []*domain.DBConnection
}

func (l listedSources) ListByCompany(context.Context, string) ([]*domain.DBConnection, error) {
	return l.rows, nil
}

// sourceVisibility is authz.Visible for sources, with a record of what it was
// asked.
type sourceVisibility struct {
	hidden   map[string]bool
	err      error
	asked    [][]string
	subjects []authz.Subject
}

func (v *sourceVisibility) Visible(_ context.Context, s authz.Subject, kind domain.ResourceKind, ids []string) ([]string, error) {
	v.asked = append(v.asked, slices.Clone(ids))
	v.subjects = append(v.subjects, s)
	if kind != domain.ResourceKindConnection {
		return nil, fmt.Errorf("asked about %s, not sources", kind)
	}
	if v.err != nil {
		return nil, v.err
	}
	out := []string{}
	for _, id := range ids {
		if !v.hidden[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func sourcesRouter(access *sourceVisibility, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := app.NewCompanyService(nil, listedSources{rows: []*domain.DBConnection{
		{ID: "src-main", CompanyID: "co-1", Label: "Toko Maju warehouse", DBType: "postgres"},
		{ID: "src-hr", CompanyID: "co-1", Label: "HR warehouse", DBType: "postgres"},
	}}, nil, nil, nil, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "u-budi")
		c.Set("role", role)
	})
	h := NewCompanyHandler(svc, nil)
	if access != nil {
		h = h.WithAccess(access)
	}
	h.Register(r.Group("/api"))
	return r
}

func listedSourceIDs(t *testing.T, r *gin.Engine) (int, []string, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/connections", nil))
	if w.Code != http.StatusOK {
		return w.Code, nil, w.Body.String()
	}
	var body struct {
		Connections []struct {
			ID string `json:"id"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v: %s", err, w.Body.String())
	}
	ids := make([]string, 0, len(body.Connections))
	for _, c := range body.Connections {
		ids = append(ids, c.ID)
	}
	return w.Code, ids, w.Body.String()
}

func TestAMembersSourceListIsNarrowedAndAnAdminsIsWhole(t *testing.T) {
	member := &sourceVisibility{hidden: map[string]bool{"src-hr": true}}
	_, ids, body := listedSourceIDs(t, sourcesRouter(member, "member"))
	if !slices.Equal(ids, []string{"src-main"}) || strings.Contains(body, "HR warehouse") {
		t.Errorf("member sees %v; want only the source they may see, and no word of the other", ids)
	}
	if len(member.asked) != 1 || member.subjects[0].UserID != "u-budi" {
		t.Errorf("asked %v for %+v, want one question for the page, for the member", member.asked, member.subjects)
	}

	// The agent form saves every ticked source, so an admin's list must stay
	// whole or a restricted source would be untied from each agent they save.
	admin := &sourceVisibility{hidden: map[string]bool{"src-hr": true}}
	_, ids, _ = listedSourceIDs(t, sourcesRouter(admin, "admin"))
	if !slices.Equal(ids, []string{"src-main", "src-hr"}) || len(admin.asked) != 0 {
		t.Errorf("admin sees %v after %d questions; want the whole list, asking nothing", ids, len(admin.asked))
	}
}

func TestASourceListCheckThatFailsServesNothing(t *testing.T) {
	code, _, body := listedSourceIDs(t, sourcesRouter(&sourceVisibility{err: errors.New("control DB down")}, "member"))
	if code != http.StatusServiceUnavailable || strings.Contains(body, "warehouse") || strings.Contains(body, "control DB") {
		t.Errorf("status %d, body %s; want a 503 with no source and no storage error", code, body)
	}
}

func TestWithNoAccessWiredEverySourceIsListed(t *testing.T) {
	_, ids, _ := listedSourceIDs(t, sourcesRouter(nil, "member"))
	if !slices.Equal(ids, []string{"src-main", "src-hr"}) {
		t.Errorf("sources = %v, want every one, as before roadmap 12", ids)
	}
}
