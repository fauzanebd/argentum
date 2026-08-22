package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
		"Use this before writing SQL when you are unsure of table or column names. " +
		"STRONGLY PREFERRED: pass `keywords` (e.g. [\"sales\",\"penjualan\",\"jual\"]) or `tables` (exact names) to get only the relevant subset — full schemas can be hundreds of tables and are expensive. " +
		"Unfiltered calls return every table and should be used only when you genuinely need a full overview. " +
		"When filtered, the response also includes `all_tables` (just names) so you can re-call with a refined filter if your guess missed."
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
		"tables": {
			Type:        "array",
			Description: "Optional exact table names to return. Case-insensitive. Use when you already know the table names; combine with `keywords` if unsure.",
			Required:    false,
			Items:       &interfaces.ParameterSpec{Type: "string"},
		},
		"keywords": {
			Type:        "array",
			Description: "Optional case-insensitive substrings; a table is included if its name OR any of its column names contains any keyword. Use domain terms in the tenant's language (e.g. [\"sales\",\"penjualan\",\"jual\"], [\"customer\",\"cust\"], [\"product\",\"barang\",\"prdcd\"]).",
			Required:    false,
			Items:       &interfaces.ParameterSpec{Type: "string"},
		},
	}
}

func (t *GetSchemaTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *GetSchemaTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		SourceID     string   `json:"source_id"`
		ForceRefresh bool     `json:"force_refresh"`
		Tables       []string `json:"tables"`
		Keywords     []string `json:"keywords"`
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
			return t.fullSchemaResponse(ctx, companyID, conns[0].ID, params.ForceRefresh, params.Tables, params.Keywords)
		}
		return formatSourceCatalog(conns), nil
	}

	source, err := ResolveSource(ctx, t.repo, companyID, params.SourceID)
	if err != nil {
		return "", err
	}
	return t.fullSchemaResponse(ctx, companyID, source.ID, params.ForceRefresh, params.Tables, params.Keywords)
}

func (t *GetSchemaTool) fullSchemaResponse(ctx context.Context, companyID, sourceID string, force bool, wantTables, keywords []string) (string, error) {
	schema, err := t.fetchSchema(ctx, companyID, sourceID, force)
	if err != nil {
		return "", err
	}

	totalTables := len(schema.Tables)
	resp := map[string]interface{}{
		"source_id":     sourceID,
		"db_type":       schema.DBType,
		"total_tables":  totalTables,
		"relationships": len(schema.Relationships),
	}

	// Filter when caller passed `tables` or `keywords`. Otherwise return the
	// full schema (back-compat) but flag the size so the model learns to
	// filter on future calls.
	if len(wantTables) > 0 || len(keywords) > 0 {
		filtered := filterSchema(schema, wantTables, keywords)
		matched := make([]string, 0, len(filtered.Tables))
		for _, t := range filtered.Tables {
			matched = append(matched, t.Name)
		}
		all := make([]string, 0, totalTables)
		for _, t := range schema.Tables {
			all = append(all, t.Name)
		}
		resp["filtered"] = true
		resp["matched_tables"] = matched
		resp["all_tables"] = all
		resp["schema"] = db.FormatSchemaForPrompt(filtered)
		resp["tables"] = len(filtered.Tables)
		if len(matched) == 0 {
			resp["hint"] = "No tables matched the supplied filter. Inspect `all_tables` and re-call with refined `tables` or `keywords`."
		}
	} else {
		resp["filtered"] = false
		resp["schema"] = db.FormatSchemaForPrompt(schema)
		resp["tables"] = totalTables
		if totalTables > 30 {
			resp["hint"] = fmt.Sprintf(
				"This source has %d tables. Next time, pass `keywords` or `tables` to get_schema so the response only includes what you need.",
				totalTables,
			)
		}
	}

	out, _ := json.Marshal(resp)
	return string(out), nil
}

// filterSchema returns a copy of s containing only tables whose name appears in
// wantTables (exact, case-insensitive) or whose name/columns contain any of
// keywords (substring, case-insensitive). Relationships are kept only when
// both endpoints survive the filter.
func filterSchema(s *db.SchemaMetadata, wantTables, keywords []string) *db.SchemaMetadata {
	exact := make(map[string]struct{}, len(wantTables))
	for _, n := range wantTables {
		n = strings.TrimSpace(strings.ToLower(n))
		if n != "" {
			exact[n] = struct{}{}
		}
	}
	kw := make([]string, 0, len(keywords))
	for _, k := range keywords {
		k = strings.TrimSpace(strings.ToLower(k))
		if k != "" {
			kw = append(kw, k)
		}
	}

	tableMatches := func(tbl db.TableInfo) bool {
		nameLower := strings.ToLower(tbl.Name)
		if _, ok := exact[nameLower]; ok {
			return true
		}
		for _, k := range kw {
			if strings.Contains(nameLower, k) {
				return true
			}
			for _, c := range tbl.Columns {
				if strings.Contains(strings.ToLower(c.Name), k) {
					return true
				}
			}
		}
		return false
	}

	out := &db.SchemaMetadata{
		DBType:      s.DBType,
		ExtractedAt: s.ExtractedAt,
	}
	kept := make(map[string]struct{}, len(s.Tables))
	for _, t := range s.Tables {
		if tableMatches(t) {
			out.Tables = append(out.Tables, t)
			kept[strings.ToLower(t.Name)] = struct{}{}
		}
	}
	for _, r := range s.Relationships {
		_, hasFrom := kept[strings.ToLower(r.FromTable)]
		_, hasTo := kept[strings.ToLower(r.ToTable)]
		if hasFrom && hasTo {
			out.Relationships = append(out.Relationships, r)
		}
	}
	return out
}

// PrefetchSourceCatalog returns the connection list for a company without
// touching any analytical DB. The worker uses it at run start to inject a
// source-catalog block into the agent's first user message.
func (t *GetSchemaTool) PrefetchSourceCatalog(ctx context.Context, companyID string) ([]*domain.DBConnection, error) {
	return t.repo.ListByCompany(ctx, companyID)
}

// FetchSchema exposes the cached schema fetch for callers outside this
// package (e.g. the embedding reindexer). Re-uses the same L1/L2 cache the
// tool uses for chat-time get_schema calls.
func (t *GetSchemaTool) FetchSchema(ctx context.Context, companyID, sourceID string, force bool) (*db.SchemaMetadata, error) {
	return t.fetchSchema(ctx, companyID, sourceID, force)
}

// fetchSchema is the cached introspection with the source's allowlist applied
// (T-H12).
//
// **The filter is here rather than in the response formatter**, so every
// consumer of a schema gets the same restricted view: the tool's own output,
// run_sql's name-error hints, the table-embedding picker. A filter applied at
// one exit is a filter the next exit does not have, and the failure that
// produces is an agent told about a table its tools will then refuse — which
// the ticket names as the most confusing failure available here.
//
// **The cache holds the unfiltered schema and the filter runs after it.** The
// allowlist is policy and can change without the schema changing; caching the
// filtered form would mean an admin widening the allowlist waits out a
// one-hour TTL, and — the direction that matters more — narrowing it would not
// take effect until the entry expired.
func (t *GetSchemaTool) fetchSchema(ctx context.Context, companyID, sourceID string, force bool) (*db.SchemaMetadata, error) {
	schema, err := t.fetchSchemaRaw(ctx, companyID, sourceID, force)
	if err != nil {
		return nil, err
	}
	list, err := t.allowlistFor(ctx, companyID, sourceID)
	if err != nil {
		return nil, err
	}
	if !list.Restricted() {
		return schema, nil
	}
	return applyAllowlist(schema, list), nil
}

// allowlistFor reads the source's allowlist, refusing a source that is not this
// company's.
//
// The ownership check is not redundant with ResolveSource. This method is
// reachable from `FetchSchema`, which several callers reach with a sourceID
// they got from somewhere else, and `GetByID` is not company-scoped — so the
// check belongs at the read rather than in each caller's discipline. Failing
// closed here costs a schema lookup; failing open would serve one tenant's
// table names to another.
func (t *GetSchemaTool) allowlistFor(ctx context.Context, companyID, sourceID string) (domain.Allowlist, error) {
	conn, err := t.repo.GetByID(ctx, sourceID)
	if err != nil {
		return domain.Allowlist{}, fmt.Errorf("resolve source for allowlist: %w", err)
	}
	if conn.CompanyID != companyID {
		return domain.Allowlist{}, fmt.Errorf("source_id %q not found for this company", sourceID)
	}
	return conn.Allowlist, nil
}

// applyAllowlist returns a copy of s holding only the tables and columns the
// allowlist permits.
//
// Relationships are kept only when both endpoints survive, which is
// filterSchema's existing rule and is load-bearing here for a different reason:
// a foreign key pointing at an excluded table would name that table in the
// prompt, which is precisely what the tenant asked not to happen.
//
// **The same rule has to be applied twice, and until 2026-08-22 it was applied
// once.** A foreign key lives in two places on this struct: the
// `Relationships` slice and the referencing column's own
// `IsForeignKey`/`ForeignKeyTable` fields — and `FormatSchemaForPrompt` prints
// both. Filtering only the slice left the model reading `customer_id (integer)
// → dim_customers.customer_id` on a source whose allowlist excluded
// `dim_customers`, which is the excluded table's name, its key column, and the
// join that reaches it. Found by the §1q live gate; the unit fixture had no
// column-level foreign keys at all, so nothing here could have caught it.
//
// Two passes, because a column can reference a table that appears later in the
// list and the answer depends on the whole set having been decided.
func applyAllowlist(s *db.SchemaMetadata, list domain.Allowlist) *db.SchemaMetadata {
	out := &db.SchemaMetadata{DBType: s.DBType, ExtractedAt: s.ExtractedAt}
	kept := make(map[string]bool, len(s.Tables))
	for _, tbl := range s.Tables {
		if list.AllowsTable(tbl.Name) {
			kept[strings.ToLower(tbl.Name)] = true
		}
	}
	for _, tbl := range s.Tables {
		if !kept[strings.ToLower(tbl.Name)] {
			continue
		}
		copied := tbl
		restricted := list.ColumnsRestricted(tbl.Name)
		// A fresh slice whenever anything changes. `s` is the cached schema
		// get_schema serves to every caller, so editing a ColumnInfo in place
		// would carry one source's restrictions into the next request.
		cols := make([]db.ColumnInfo, 0, len(tbl.Columns))
		for _, col := range tbl.Columns {
			if restricted && !list.AllowsColumn(tbl.Name, col.Name) {
				continue
			}
			if col.IsForeignKey && !kept[strings.ToLower(col.ForeignKeyTable)] {
				col.IsForeignKey = false
				col.ForeignKeyTable = ""
				col.ForeignKeyColumn = ""
			}
			cols = append(cols, col)
		}
		copied.Columns = cols
		out.Tables = append(out.Tables, copied)
	}
	for _, rel := range s.Relationships {
		if kept[strings.ToLower(rel.FromTable)] && kept[strings.ToLower(rel.ToTable)] {
			out.Relationships = append(out.Relationships, rel)
		}
	}
	return out
}

func (t *GetSchemaTool) fetchSchemaRaw(ctx context.Context, companyID, sourceID string, force bool) (*db.SchemaMetadata, error) {
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
