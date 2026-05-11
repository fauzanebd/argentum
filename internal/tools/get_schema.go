package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// GetSchemaTool returns the schema of a tenant's analytical database. With no
// source_id, it returns the source catalog (id, label, db_type, description)
// so the agent can choose; with a source_id, it returns that source's full
// schema. Schemas are cached per (companyID, sourceID) for cacheTTL to avoid
// hammering information_schema on every call. When a Redis client is provided
// the cache is shared across worker replicas and survives restarts; otherwise
// it falls back to an in-process map.
type GetSchemaTool struct {
	pool     *db.TenantConnPool
	repo     domain.ConnectionRepository
	cacheTTL time.Duration

	rdb *redis.Client

	mu    sync.Mutex
	cache map[string]schemaCacheEntry
}

type schemaCacheEntry struct {
	at     time.Time
	schema *db.SchemaMetadata
}

// NewGetSchemaTool builds a tool with an in-memory schema cache only. Prefer
// NewGetSchemaToolWithRedis in production so the cache survives worker restarts
// and is shared across replicas.
func NewGetSchemaTool(pool *db.TenantConnPool, repo domain.ConnectionRepository) *GetSchemaTool {
	return &GetSchemaTool{
		pool:     pool,
		repo:     repo,
		cacheTTL: 1 * time.Hour,
		cache:    make(map[string]schemaCacheEntry),
	}
}

// NewGetSchemaToolWithRedis returns a tool that primarily caches schema in
// Redis (shared, persistent) and uses the in-memory map as a tiny L1.
func NewGetSchemaToolWithRedis(pool *db.TenantConnPool, repo domain.ConnectionRepository, rdb *redis.Client) *GetSchemaTool {
	t := NewGetSchemaTool(pool, repo)
	t.rdb = rdb
	return t
}

func schemaCacheKey(companyID, sourceID string) string {
	return "schema:" + companyID + ":" + sourceID
}

func (t *GetSchemaTool) Name() string { return "get_schema" }

func (t *GetSchemaTool) Description() string {
	return "Get the catalog of available data sources, or the schema of a specific source. " +
		"Without source_id, returns the list of sources for this organization (each with id, label, db_type, description) — call again with a source_id to get that source's tables/columns/relationships. " +
		"Use this before writing SQL when you are unsure of table or column names."
}

func (t *GetSchemaTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"source_id": {
			Type:        "string",
			Description: "ID of the data source to introspect. Omit to receive the source catalog instead of a full schema.",
			Required:    false,
		},
		"force_refresh": {
			Type:        "boolean",
			Description: "If true, bypass the schema cache and re-introspect the source.",
			Required:    false,
		},
	}
}

func (t *GetSchemaTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *GetSchemaTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		SourceID     string `json:"source_id"`
		ForceRefresh bool   `json:"force_refresh"`
	}
	_ = json.Unmarshal([]byte(args), &params)

	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return "", fmt.Errorf("no tenant in context: cannot resolve database connection")
	}

	if params.SourceID == "" {
		conns, err := t.repo.ListByCompany(ctx, companyID)
		if err != nil {
			return "", fmt.Errorf("list sources: %w", err)
		}
		if len(conns) == 1 {
			// One-source convenience: skip the catalog round-trip.
			return t.fullSchemaResponse(ctx, companyID, conns[0].ID, params.ForceRefresh)
		}
		return formatSourceCatalog(conns), nil
	}

	source, err := ResolveSource(ctx, t.repo, companyID, params.SourceID)
	if err != nil {
		return "", err
	}
	return t.fullSchemaResponse(ctx, companyID, source.ID, params.ForceRefresh)
}

func (t *GetSchemaTool) fullSchemaResponse(ctx context.Context, companyID, sourceID string, force bool) (string, error) {
	schema, err := t.fetchSchema(ctx, companyID, sourceID, force)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]interface{}{
		"source_id":     sourceID,
		"schema":        db.FormatSchemaForPrompt(schema),
		"tables":        len(schema.Tables),
		"relationships": len(schema.Relationships),
		"db_type":       schema.DBType,
	})
	return string(out), nil
}

// PrefetchSourceCatalog returns the connection list for a company without
// touching any analytical DB. The worker uses it at run start to inject a
// source-catalog block into the agent's first user message.
func (t *GetSchemaTool) PrefetchSourceCatalog(ctx context.Context, companyID string) ([]*domain.DBConnection, error) {
	return t.repo.ListByCompany(ctx, companyID)
}

func (t *GetSchemaTool) fetchSchema(ctx context.Context, companyID, sourceID string, force bool) (*db.SchemaMetadata, error) {
	key := companyID + ":" + sourceID
	if !force {
		// L1: in-process map (microsecond hit when the same worker just served this).
		t.mu.Lock()
		if e, ok := t.cache[key]; ok && time.Since(e.at) < t.cacheTTL {
			t.mu.Unlock()
			return e.schema, nil
		}
		t.mu.Unlock()

		// L2: Redis (shared across workers, survives restarts).
		if t.rdb != nil {
			if data, err := t.rdb.Get(ctx, schemaCacheKey(companyID, sourceID)).Bytes(); err == nil {
				var schema db.SchemaMetadata
				if jerr := json.Unmarshal(data, &schema); jerr == nil {
					t.mu.Lock()
					t.cache[key] = schemaCacheEntry{at: time.Now(), schema: &schema}
					t.mu.Unlock()
					return &schema, nil
				} else {
					logrus.WithError(jerr).Warn("schema redis cache unmarshal failed; refetching")
				}
			} else if err != redis.Nil {
				logrus.WithError(err).Warn("schema redis cache read failed; falling back to introspection")
			}
		}
	}

	conn, err := t.pool.For(ctx, companyID, sourceID)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant connection: %w", err)
	}
	schema, err := conn.ExtractSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("extract schema: %w", err)
	}

	t.mu.Lock()
	t.cache[key] = schemaCacheEntry{at: time.Now(), schema: schema}
	t.mu.Unlock()

	if t.rdb != nil {
		if data, jerr := json.Marshal(schema); jerr == nil {
			if rerr := t.rdb.Set(ctx, schemaCacheKey(companyID, sourceID), data, t.cacheTTL).Err(); rerr != nil {
				logrus.WithError(rerr).Warn("schema redis cache write failed")
			}
		}
	}
	return schema, nil
}

// Invalidate drops the cached schema for a single (company, source). Used
// after the user rotates that connection's DSN.
func (t *GetSchemaTool) Invalidate(companyID, sourceID string) {
	key := companyID + ":" + sourceID
	t.mu.Lock()
	delete(t.cache, key)
	t.mu.Unlock()
	if t.rdb != nil {
		if err := t.rdb.Del(context.Background(), schemaCacheKey(companyID, sourceID)).Err(); err != nil {
			logrus.WithError(err).Warn("schema redis cache invalidate failed")
		}
	}
}

// InvalidateAll drops every cached schema for a company.
func (t *GetSchemaTool) InvalidateAll(companyID string) {
	prefix := companyID + ":"
	t.mu.Lock()
	for k := range t.cache {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(t.cache, k)
		}
	}
	t.mu.Unlock()
	if t.rdb != nil {
		ctx := context.Background()
		pattern := "schema:" + companyID + ":*"
		iter := t.rdb.Scan(ctx, 0, pattern, 100).Iterator()
		var keys []string
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		if err := iter.Err(); err != nil {
			logrus.WithError(err).Warn("schema redis cache scan failed")
		}
		if len(keys) > 0 {
			if err := t.rdb.Del(ctx, keys...).Err(); err != nil {
				logrus.WithError(err).Warn("schema redis cache bulk-invalidate failed")
			}
		}
	}
}

// formatSourceCatalog renders the catalog response the agent receives when
// it calls get_schema without a source_id.
func formatSourceCatalog(conns []*domain.DBConnection) string {
	type sourceRow struct {
		ID          string `json:"id"`
		Label       string `json:"label,omitempty"`
		DBType      string `json:"db_type"`
		Description string `json:"description,omitempty"`
		IsDefault   bool   `json:"is_default"`
	}
	rows := make([]sourceRow, 0, len(conns))
	for _, c := range conns {
		rows = append(rows, sourceRow{
			ID:          c.ID,
			Label:       c.Label,
			DBType:      c.DBType,
			Description: c.Description,
			IsDefault:   c.IsDefault,
		})
	}
	out, _ := json.Marshal(map[string]interface{}{
		"sources": rows,
		"note":    "Pick a source_id and call get_schema again with it to retrieve that source's tables and columns.",
	})
	return string(out)
}
