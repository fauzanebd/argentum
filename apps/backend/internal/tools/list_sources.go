package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// ListSourcesTool returns the catalog of analytical databases registered for
// the current tenant. The agent uses it to discover which source_id to pass
// to get_schema and run_sql.
type ListSourcesTool struct {
	repo domain.ConnectionRepository
}

func NewListSourcesTool(repo domain.ConnectionRepository) *ListSourcesTool {
	return &ListSourcesTool{repo: repo}
}

func (t *ListSourcesTool) Name() string { return "list_sources" }

func (t *ListSourcesTool) Description() string {
	return "List the data sources (analytical databases) registered for this organization. " +
		"Returns each source's id, label, db_type, description, origin, and is_default flag. " +
		"origin is 'tenant' for a database this organization connected, or 'document' for tables " +
		"extracted from PDFs it uploaded and a person reviewed — a document source's rows carry " +
		"source_page and source_row naming the page each figure was read from. " +
		"Use this when you need to choose a source_id for run_sql, get_schema, or create_dashboard."
}

func (t *ListSourcesTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{}
}

func (t *ListSourcesTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *ListSourcesTool) Execute(ctx context.Context, _ string) (string, error) {
	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return "", fmt.Errorf("no tenant in context")
	}
	conns, err := t.repo.ListByCompany(ctx, companyID)
	if err != nil {
		return "", fmt.Errorf("list sources: %w", err)
	}
	// The catalog this tool returns is a menu the model then orders from, so it
	// is scoped for the same reason ResolveSource is (T-S2): an agent that is
	// told about the HR database and then refused it every time it asks is
	// worse than one that never hears of it. Empty allowlist returns everything.
	conns = agentscope.FromContext(ctx).FilterSources(conns)
	type sourceRow struct {
		ID          string `json:"id"`
		Label       string `json:"label,omitempty"`
		DBType      string `json:"db_type"`
		Description string `json:"description,omitempty"`
		// Origin says whether this source is a system of record or this
		// product's reading of a document (T-P6). It reaches the model because
		// the right thing to do when two sources disagree depends on which of
		// them is derived — and because a figure that came out of a PDF can be
		// traced back to its page, which is worth knowing before answering.
		Origin    string `json:"origin"`
		IsDefault bool   `json:"is_default"`
	}
	rows := make([]sourceRow, 0, len(conns))
	for _, c := range conns {
		rows = append(rows, sourceRow{
			ID:          c.ID,
			Label:       c.Label,
			DBType:      c.DBType,
			Description: c.Description,
			Origin:      originOrDefault(c.Origin),
			IsDefault:   c.IsDefault,
		})
	}
	out, _ := json.Marshal(map[string]interface{}{
		"sources": rows,
	})
	return string(out), nil
}

// originOrDefault fills in the origin for a row written before migration 060.
// Every such row is a database somebody connected, which is what 'tenant'
// means; an empty string reaching the model would be a third value it has to
// interpret.
func originOrDefault(origin string) string {
	if origin == "" {
		return domain.OriginTenant
	}
	return origin
}
