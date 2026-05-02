package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// GetSchemaTool returns the schema of the *current tenant's* connected
// database. Result is cached per company for cacheTTL to avoid hammering
// information_schema on every call.
type GetSchemaTool struct {
	pool     *db.TenantConnPool
	cacheTTL time.Duration

	mu    sync.Mutex
	cache map[string]schemaCacheEntry
}

type schemaCacheEntry struct {
	at     time.Time
	schema *db.SchemaMetadata
}

func NewGetSchemaTool(pool *db.TenantConnPool) *GetSchemaTool {
	return &GetSchemaTool{
		pool:     pool,
		cacheTTL: 1 * time.Hour,
		cache:    make(map[string]schemaCacheEntry),
	}
}

func (t *GetSchemaTool) Name() string { return "get_schema" }

func (t *GetSchemaTool) Description() string {
	return "Get database schema information including tables, columns, and relationships. Use this when you need to understand the database structure before writing SQL."
}

func (t *GetSchemaTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"force_refresh": {
			Type:        "boolean",
			Description: "If true, bypass the schema cache and re-introspect.",
			Required:    false,
		},
	}
}

func (t *GetSchemaTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *GetSchemaTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		ForceRefresh bool `json:"force_refresh"`
	}
	_ = json.Unmarshal([]byte(args), &params)

	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return "", fmt.Errorf("no tenant in context: cannot resolve database connection")
	}

	schema, err := t.fetchSchema(ctx, companyID, params.ForceRefresh)
	if err != nil {
		return "", err
	}

	out, _ := json.Marshal(map[string]interface{}{
		"schema":             db.FormatSchemaForPrompt(schema),
		"tables":             len(schema.Tables),
		"relationships":      len(schema.Relationships),
		"db_type":            schema.DBType,
	})
	return string(out), nil
}

// PrefetchSchema is called by the worker before each agent run to warm the
// cache and to inject the schema into the system prompt without spending a
// tool-call iteration on it.
func (t *GetSchemaTool) PrefetchSchema(ctx context.Context, companyID string) (*db.SchemaMetadata, error) {
	return t.fetchSchema(ctx, companyID, false)
}

func (t *GetSchemaTool) fetchSchema(ctx context.Context, companyID string, force bool) (*db.SchemaMetadata, error) {
	if !force {
		t.mu.Lock()
		if e, ok := t.cache[companyID]; ok && time.Since(e.at) < t.cacheTTL {
			t.mu.Unlock()
			return e.schema, nil
		}
		t.mu.Unlock()
	}

	conn, err := t.pool.For(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant connection: %w", err)
	}
	schema, err := conn.ExtractSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("extract schema: %w", err)
	}

	t.mu.Lock()
	t.cache[companyID] = schemaCacheEntry{at: time.Now(), schema: schema}
	t.mu.Unlock()
	return schema, nil
}

// Invalidate drops the cached schema for a company. Call after the user
// changes their DSN.
func (t *GetSchemaTool) Invalidate(companyID string) {
	t.mu.Lock()
	delete(t.cache, companyID)
	t.mu.Unlock()
}
