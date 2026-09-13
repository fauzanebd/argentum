package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z8, decision 9: "An API key is a machine, not a person. It gets an optional
// agent allowlist of its own, beside T-13's scopes."

func TestAKeysAgentListIsCheckedAgainstTheRosterAndStoredNormalised(t *testing.T) {
	repo := newStubAPIKeys()
	svc := NewAPIKeyService(repo).WithAgents(rosterWith(
		&domain.Agent{ID: "ag-fin", CompanyID: "co-1"},
		&domain.Agent{ID: "ag-theirs", CompanyID: "co-2"},
	))

	res, err := svc.Create(context.Background(), "co-1", "user-1", "Finance job",
		[]string{"write:chat"}, []string{" AG-FIN ", "ag-fin"}, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !slices.Equal(res.Key.AgentIDs, []string{"ag-fin"}) {
		t.Errorf("AgentIDs = %v, want one lower-cased, de-duplicated id", res.Key.AgentIDs)
	}

	// Another company's agent reads as an agent this workspace does not have.
	if _, err := svc.Create(context.Background(), "co-1", "user-1", "Leak",
		[]string{"write:chat"}, []string{"ag-theirs"}, 0); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("a list naming another company's agent = %v, want ErrInvalidInput", err)
	}
	if len(repo.byPrefix) != 1 {
		t.Errorf("stored %d keys, want only the valid one", len(repo.byPrefix))
	}

	// No list is every agent, and never a nil slice: pq sends nil as NULL.
	open, err := svc.Create(context.Background(), "co-1", "user-1", "Everything", []string{"write:chat"}, nil, 0)
	if err != nil || open.Key.AgentIDs == nil || len(open.Key.AgentIDs) != 0 {
		t.Errorf("a key with no list = (%v, %v), want an empty, non-nil list", open.Key.AgentIDs, err)
	}
}

func TestAnEmptyAgentListAllowsEveryAgentAndAListOnlyItsOwn(t *testing.T) {
	cases := []struct {
		list  []string
		agent string
		want  bool
	}{
		{nil, "ag-hr", true},
		{[]string{}, "ag-hr", true},
		{[]string{"ag-fin"}, "ag-fin", true},
		{[]string{"ag-fin"}, "AG-FIN", true},
		{[]string{"ag-fin"}, "ag-hr", false},
		// What an unpinned turn in a company with no roster runs as: no agent,
		// which no list names.
		{[]string{"ag-fin"}, "", false},
	}
	for _, tc := range cases {
		if got := domain.KeyAllowsAgent(tc.list, tc.agent); got != tc.want {
			t.Errorf("KeyAllowsAgent(%v, %q) = %v, want %v", tc.list, tc.agent, got, tc.want)
		}
	}
}
