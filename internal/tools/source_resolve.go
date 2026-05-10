package tools

import (
	"context"
	"fmt"
	"strings"

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
//   - >1 connections, empty requestedID → error listing available sources;
//     the agent reads the menu in the tool error and retries with an id.
//   - >1 connections, non-empty requestedID → validate ownership.
func ResolveSource(ctx context.Context, repo domain.ConnectionRepository, companyID, requestedID string) (*domain.DBConnection, error) {
	if companyID == "" {
		return nil, fmt.Errorf("companyID is required")
	}
	conns, err := repo.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	if len(conns) == 0 {
		return nil, fmt.Errorf("no DB connection registered for this company; ask the user to connect a database in settings")
	}
	if requestedID == "" {
		if len(conns) == 1 {
			return conns[0], nil
		}
		return nil, fmt.Errorf("multiple data sources available; specify source_id. Available: %s",
			formatSourceMenu(conns))
	}
	for _, c := range conns {
		if c.ID == requestedID {
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
