package handlers

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// RIZ-76 R5 / Q2: `GET /api/agents` carries the default agent's starter
// questions, built from the company's source profiles, and never fails the
// roster over them.

// stubSourceProfiles answers ListByCompany from a fixture, or with err.
type stubSourceProfiles struct {
	list []*domain.SourceProfile
	err  error
}

func (s stubSourceProfiles) GetByConnection(context.Context, string, string) (*domain.SourceProfile, error) {
	return nil, domain.ErrNotFound
}
func (s stubSourceProfiles) ListByCompany(context.Context, string) ([]*domain.SourceProfile, error) {
	return s.list, s.err
}
func (s stubSourceProfiles) Upsert(context.Context, *domain.SourceProfile) error { return nil }

// hrSourceRestricted is a grant store in which the conn-hr source is
// restricted and nobody holds it. Agents are all open.
type hrSourceRestricted struct{ err error }

func (h hrSourceRestricted) Visible(_ context.Context, _ authz.Subject, kind domain.ResourceKind, ids []string) ([]string, error) {
	if h.err != nil && kind == domain.ResourceKindConnection {
		return nil, h.err
	}
	out := []string{}
	for _, id := range ids {
		if kind != domain.ResourceKindConnection || id != "conn-hr" {
			out = append(out, id)
		}
	}
	return out, nil
}

// twoSources is a default warehouse first, then an HR source.
func twoSources() []*domain.SourceProfile {
	return []*domain.SourceProfile{
		{ConnectionID: "conn-wh", CompanyID: "co-1", Entities: []domain.SourceEntity{
			{Table: "orders", Means: "a sale"},
			{Table: "customers", Means: "a buyer"},
		}},
		{ConnectionID: "conn-hr", CompanyID: "co-1", Entities: []domain.SourceEntity{
			{Table: "salaries", Means: "a pay line"},
		}},
	}
}

func starterRouter(t *testing.T, profiles domain.SourceProfileRepository, access AgentAccess, role string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "u-1")
		c.Set("role", role)
		c.Next()
	})
	h := NewAgentsHandler(app.NewAgentService(newFakeAgentRepo(), nil, nil))
	if access != nil {
		h = h.WithAccess(access)
	}
	if profiles != nil {
		h = h.WithSourceProfiles(profiles)
	}
	h.Register(r.Group(""))
	return r
}

func TestRosterCarriesTheDefaultAgentsStarterQuestions(t *testing.T) {
	cases := []struct {
		name     string
		profiles domain.SourceProfileRepository
		access   AgentAccess
		role     string
		want     []string
	}{
		{
			name:     "a member with every source open is asked about the inferred tables (AC6)",
			profiles: stubSourceProfiles{list: twoSources()},
			role:     "member",
			want: []string{
				"Give me an overview of our orders data.",
				"What are the most recent records in customers?",
				"What stands out in salaries?",
			},
		},
		{
			name:     "a member is never asked about a table in a source restricted from them",
			profiles: stubSourceProfiles{list: twoSources()},
			access:   hrSourceRestricted{},
			role:     "member",
			want: []string{
				"Give me an overview of our orders data.",
				"What are the most recent records in customers?",
			},
		},
		{
			name:     "an admin is asked about every source, as Settings shows them every source",
			profiles: stubSourceProfiles{list: twoSources()},
			access:   hrSourceRestricted{},
			role:     "admin",
			want: []string{
				"Give me an overview of our orders data.",
				"What are the most recent records in customers?",
				"What stands out in salaries?",
			},
		},
		{
			name:     "no profile yet sends the generic three (AC7)",
			profiles: stubSourceProfiles{},
			role:     "member",
			want: []string{
				"What data can you see in my workspace?",
				"Which tables matter most for my business, and what is in them?",
				"Summarize what my main database is about.",
			},
		},
		{
			name:     "a failed profile read sends none, and the roster still answers",
			profiles: stubSourceProfiles{err: errors.New("pq: relation source_profiles does not exist")},
			role:     "member",
			want:     []string{},
		},
		{
			name:     "a failed source access check sends none rather than the unfiltered tables",
			profiles: stubSourceProfiles{list: twoSources()},
			access:   hrSourceRestricted{err: errors.New("grant store down")},
			role:     "member",
			want:     []string{},
		},
		{
			name: "no repository wired sends none",
			role: "member",
			want: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body, raw := getRoster(t, starterRouter(t, tc.profiles, tc.access, tc.role))
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200 — the questions must never fail the roster: %s", code, raw)
			}
			if !slices.Equal(body.DataStarterQuestions, tc.want) {
				t.Errorf("data_starter_questions = %q\nwant %q", body.DataStarterQuestions, tc.want)
			}
			// Never null on the wire: the dashboard reads a missing list as an
			// old API, and a null one would be a third case nobody wrote.
			if !strings.Contains(raw, `"data_starter_questions":[`) {
				t.Errorf("data_starter_questions is not a list: %s", raw)
			}
			if strings.Contains(raw, "pq:") || strings.Contains(raw, "grant store") {
				t.Errorf("the roster leaked an internal error: %s", raw)
			}
			if len(body.Agents) == 0 {
				t.Errorf("the roster lost its agents: %s", raw)
			}
		})
	}
}
