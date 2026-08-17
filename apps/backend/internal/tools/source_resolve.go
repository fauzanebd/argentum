package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
)

// ResolveSource picks which DB connection a tool call targets, validating
// tenant ownership and forcing the agent to be explicit when more than one
// source exists. Returns the chosen *domain.DBConnection so the caller has
// the db_type, label, etc. without another lookup.
//
// Behaviour:
//   - 0 connections → "no DB connection registered" error.
//   - 1 connection,  empty requestedID → use it.
//   - 1 connection,  non-empty requestedID → validate ownership.
//   - >1 connections, empty requestedID → the source this turn already
//     resolved, if there is one and it is still in the allowed catalog;
//     otherwise an error listing available sources.
//   - >1 connections, non-empty requestedID → validate ownership.
//
// The turn-scoped reuse was added 2026-08-14 for the retry loop in
// coverage/eval-sprint1.md §4: the menu is a fine answer to "which source?"
// and a useless one to an agent that already picked, which called the tool
// again unchanged until its iteration budget ran out. It is narrow on purpose,
// and the two limits are what keep it honest — it only ever answers with an id
// this turn already resolved, and that id is re-checked against the filtered
// catalog below, so it can never widen what the roster allows. A turn that has
// touched no source still gets the menu.
//
// This is the choke point every data tool goes through — get_schema, run_sql
// and create_dashboard all call it — which is why the roster's source
// allowlist is enforced here rather than in a persona (T-S2). The scope is
// applied to the catalog *before* any other branch, so an agent scoped to one
// source sees a one-source world: the "specify source_id" menu lists only what
// it may reach, and an out-of-scope id gets the same "not found for this
// company" it would get for another tenant's id.
//
// That sameness is deliberate. A distinct "not allowed for this agent" error
// would tell a prompt-injected model exactly what to probe for, and the id it
// was handed came from the model rather than from the user.
func ResolveSource(ctx context.Context, repo domain.ConnectionRepository, companyID, requestedID string) (*domain.DBConnection, error) {
	if companyID == "" {
		return nil, fmt.Errorf("companyID is required")
	}
	conns, err := repo.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	conns = agentscope.FromContext(ctx).FilterSources(conns)
	if len(conns) == 0 {
		return nil, fmt.Errorf("no DB connection registered for this company; ask the user to connect a database in settings")
	}
	if requestedID == "" {
		if len(conns) == 1 {
			rememberSource(ctx, conns[0].ID)
			return conns[0], nil
		}
		// Continue against the source this turn already chose. The lookup runs
		// over `conns`, which is post-filter, so a remembered id the roster no
		// longer allows simply is not found and the menu is shown instead.
		if prior := recalledSource(ctx); prior != "" {
			for _, c := range conns {
				if c.ID == prior {
					return c, nil
				}
			}
		}
		return nil, fmt.Errorf("multiple data sources available; specify source_id. Available: %s",
			formatSourceMenu(conns))
	}
	for _, c := range conns {
		if c.ID == requestedID {
			rememberSource(ctx, c.ID)
			return c, nil
		}
	}
	return nil, fmt.Errorf("source_id %q not found for this company. Available: %s",
		requestedID, formatSourceMenu(conns))
}

func formatSourceMenu(conns []*domain.DBConnection) string {
	parts := make([]string, 0, len(conns))
	for _, c := range conns {
		label := c.Label
		if label == "" {
			label = "(unlabelled)"
		}
		parts = append(parts, fmt.Sprintf("%s=%s [%s]", c.ID, label, c.DBType))
	}
	return strings.Join(parts, ", ")
}
