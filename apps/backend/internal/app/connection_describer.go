package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
)

// ConnectionDescriberLLM is the narrow LLM contract the describer needs:
// just one-shot text generation. The agent SDK's interfaces.LLM satisfies it.
type ConnectionDescriberLLM interface {
	Generate(ctx context.Context, prompt string, opts ...interfaces.GenerateOption) (string, error)
}

// ConnectionDescriber introspects a tenant connection's schema and uses an LLM
// to write a one-paragraph summary of what the database appears to contain,
// then persists it onto the row. Manual descriptions (description_source =
// "manual") are never overwritten. Errors are returned but callers should
// treat them as best-effort: the autogen runs in a background goroutine.
type ConnectionDescriber struct {
	llm  ConnectionDescriberLLM
	pool *db.TenantConnPool
	repo domain.ConnectionRepository
}

func NewConnectionDescriber(llm ConnectionDescriberLLM, pool *db.TenantConnPool, repo domain.ConnectionRepository) *ConnectionDescriber {
	return &ConnectionDescriber{llm: llm, pool: pool, repo: repo}
}

const describeSystemPrompt = `You write very short, factual descriptions of analytical databases. ` +
	`Given a list of tables and a sample of their columns, return ONE sentence (max 200 characters, plain text, no markdown, no preamble) describing what the database appears to contain. ` +
	`Examples: "Customers, orders, and refunds for a B2C retail store." / "Employee records, payroll runs, and attendance logs."`

const describeMaxChars = 500

// Describe generates and persists a description for one connection. Safe to
// call from a goroutine — does its own context. If the connection's
// description_source is already "manual" it is left untouched.
func (d *ConnectionDescriber) Describe(ctx context.Context, companyID, sourceID string) error {
	_, err := d.describe(ctx, companyID, sourceID, false)
	return err
}

// Regenerate forces a fresh autogen for one connection, overwriting any
// existing description regardless of description_source. Returns the
// resulting description text. Intended for the admin-triggered
// regenerate-description endpoint.
func (d *ConnectionDescriber) Regenerate(ctx context.Context, companyID, sourceID string) (string, error) {
	return d.describe(ctx, companyID, sourceID, true)
}

// describe is the shared core. With force=false (Describe) it skips when the
// row is already manual, both at entry and right before persisting. With
// force=true (Regenerate) it overwrites unconditionally. Tenant ownership is
// always validated.
func (d *ConnectionDescriber) describe(ctx context.Context, companyID, sourceID string, force bool) (string, error) {
	if d == nil || d.llm == nil {
		if force {
			return "", fmt.Errorf("description regeneration is unavailable: LLM not configured")
		}
		return "", nil
	}
	conn, err := d.repo.GetByID(ctx, sourceID)
	if err != nil {
		return "", fmt.Errorf("get connection: %w", err)
	}
	if conn.CompanyID != companyID {
		return "", domain.ErrUnauthorized
	}
	if !force && conn.DescriptionSource == domain.DescriptionSourceManual {
		return "", nil
	}
	pooled, err := d.pool.For(ctx, companyID, sourceID)
	if err != nil {
		return "", fmt.Errorf("dial source: %w", err)
	}
	schema, err := pooled.ExtractSchema(ctx)
	if err != nil {
		return "", fmt.Errorf("extract schema: %w", err)
	}
	prompt := buildDescribePrompt(schema)
	resp, err := d.llm.Generate(ctx, prompt,
		interfaces.WithSystemMessage(describeSystemPrompt),
		interfaces.WithTemperature(0.2),
	)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	desc := sanitizeDescription(resp)
	if desc == "" {
		return "", fmt.Errorf("llm returned empty description")
	}

	// Re-fetch in non-force mode to avoid clobbering a manual edit that
	// landed during the LLM call. In force mode the admin is intentionally
	// overwriting, so we skip the abort.
	latest, err := d.repo.GetByID(ctx, sourceID)
	if err != nil {
		return "", fmt.Errorf("re-fetch connection: %w", err)
	}
	if !force && latest.DescriptionSource == domain.DescriptionSourceManual {
		return "", nil
	}
	latest.Description = desc
	latest.DescriptionSource = domain.DescriptionSourceAuto
	if err := d.repo.Update(ctx, latest); err != nil {
		return "", fmt.Errorf("persist description: %w", err)
	}
	return desc, nil
}

// DescribeAsync kicks off Describe in a background goroutine with its own
// timeout-bounded context. Errors are logged.
func (d *ConnectionDescriber) DescribeAsync(companyID, sourceID string) {
	if d == nil || d.llm == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := d.Describe(ctx, companyID, sourceID); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": companyID,
				"source_id":  sourceID,
			}).Warn("connection describer failed; description will stay empty until next trigger")
		}
	}()
}

// buildDescribePrompt returns a compact representation of the schema suitable
// for a single LLM call: table names + the first ~30 column names per table,
// hard-capped to keep the prompt small.
func buildDescribePrompt(schema *db.SchemaMetadata) string {
	const maxTables = 40
	const maxCols = 30
	var b strings.Builder
	b.WriteString("Database type: ")
	b.WriteString(schema.DBType)
	b.WriteString("\n\nTables:\n")
	tables := schema.Tables
	if len(tables) > maxTables {
		tables = tables[:maxTables]
	}
	for _, tab := range tables {
		b.WriteString("- ")
		b.WriteString(tab.Name)
		cols := tab.Columns
		if len(cols) > maxCols {
			cols = cols[:maxCols]
		}
		if len(cols) > 0 {
			b.WriteString(" (")
			for i, col := range cols {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(col.Name)
			}
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if len(schema.Tables) > maxTables {
		fmt.Fprintf(&b, "...and %d more tables\n", len(schema.Tables)-maxTables)
	}
	b.WriteString("\nReturn one short sentence summarising what this database contains.")
	return b.String()
}

// sanitizeDescription trims, removes line breaks, and hard-caps the LLM
// response to a sensible length.
func sanitizeDescription(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	if len(s) > describeMaxChars {
		s = s[:describeMaxChars]
	}
	return s
}
