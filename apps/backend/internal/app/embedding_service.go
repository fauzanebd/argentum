package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/llmtenant"
	"github.com/fauzanebd/argentum/internal/tools"
)

// noiseTablePatterns matches archive/backup/temp/dated table variants that
// share column lists with their live counterparts. Cosine similarity treats
// them as near-duplicates and dilutes top-K signal, so we skip them at index
// time. Patterns are case-insensitive and cover common SQL Server conventions
// observed in tenant data (Bahasa month names included).
var noiseTablePatterns = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`^Backup_`,
	`^Deleted_`,
	`^Temp_`,
	`^tbTemp`,
	`_[0-9]{6}$`,
	`_[0-9]{4}$`,
	`_(Jan|Feb|Mar|Apr|Mei|Jun|Jul|Agu|Sep|Okt|Nov|Des)_[0-9]{4}$`,
	`_Daily_[0-9]+$`,
	`_LKSO[0-9_]+`,
	`^sysdiagrams$`,
	`^DataBarang(Salah|TidakPLU)$`,
	`^DataHargaBermasalah$`,
	`^DataTransaksi(GantiPLU|TidakPLU)$`,
}, `|`))

func isNoiseTable(name string) bool {
	return noiseTablePatterns.MatchString(name)
}

// maxColumnsInDoc caps how many columns we include per table when building
// the embedding doc string. Most retrieval signal comes from table name +
// description + the first dozen-or-so columns; flat-out dumping 300-column
// monsters wastes embedding tokens and skews similarity.
const maxColumnsInDoc = 30

// embeddingIndexedMarker is the connection-repository capability we need
// to mark a source's last reindex time. Wrapped in a tiny interface so
// the service stays testable without the full repo.
type embeddingIndexedMarker interface {
	MarkEmbeddingsIndexed(ctx context.Context, id string, at time.Time) error
}

// EmbeddingService rebuilds the per-source table-embedding index. Triggered
// manually by the admin endpoint; not run on schema fetch (per design).
type EmbeddingService struct {
	conns      domain.ConnectionRepository
	connMarker embeddingIndexedMarker
	embRepo    domain.TableEmbeddingRepository
	schemaTool *tools.GetSchemaTool
	embedCache *llmtenant.EmbeddingCache
}

// NewEmbeddingService wires the dependencies. schemaTool reuses the
// process-local + Redis schema cache to avoid re-introspecting a database
// the tool just read. embedCache resolves the per-tenant embedding client
// at reindex time so each company hits its own provider/key.
func NewEmbeddingService(
	conns domain.ConnectionRepository,
	connMarker embeddingIndexedMarker,
	embRepo domain.TableEmbeddingRepository,
	schemaTool *tools.GetSchemaTool,
	embedCache *llmtenant.EmbeddingCache,
) *EmbeddingService {
	return &EmbeddingService{
		conns:      conns,
		connMarker: connMarker,
		embRepo:    embRepo,
		schemaTool: schemaTool,
		embedCache: embedCache,
	}
}

// ReindexResult is the structured return surfaced to the admin endpoint.
type ReindexResult struct {
	Tables    int           `json:"tables"`
	Skipped   int           `json:"skipped_noise"`
	Duration  time.Duration `json:"-"`
	IndexedAt time.Time     `json:"indexed_at"`
}

// RetrievalHit is a single semantic match — table name + cosine distance.
type RetrievalHit struct {
	Table    string  `json:"table"`
	Distance float32 `json:"distance"`
}

// RetrievalResult is the response shape for the test-RAG endpoint. It bundles
// the embedding hits, the filtered get_schema output the agent would receive,
// per-step timings, and table counts — enough to diagnose whether the picker
// is producing useful shortlists without going through a chat turn.
type RetrievalResult struct {
	Query            string         `json:"query"`
	SourceID         string         `json:"source_id"`
	TopK             int            `json:"top_k"`
	Hits             []RetrievalHit `json:"hits"`
	TotalTables      int            `json:"total_tables"`
	IndexedTables    int            `json:"indexed_tables"`
	FilteredTables   int            `json:"filtered_tables"`
	SchemaPreview    string         `json:"schema_preview"`
	EmbedDurationMs  int64          `json:"embed_duration_ms"`
	TopKDurationMs   int64          `json:"topk_duration_ms"`
	SchemaDurationMs int64          `json:"schema_duration_ms"`
}

// ReindexSource force-fetches the source's schema, embeds every table's
// doc string, and atomically swaps the table_embeddings rows for that
// source. Returns (rows-written, elapsed, error). Re-runs are idempotent.
func (s *EmbeddingService) ReindexSource(ctx context.Context, companyID, sourceID string) (ReindexResult, error) {
	start := time.Now()

	conn, err := s.conns.GetByID(ctx, sourceID)
	if err != nil {
		return ReindexResult{}, err
	}
	if conn.CompanyID != companyID {
		return ReindexResult{}, domain.ErrUnauthorized
	}

	embClient, err := s.embedCache.For(ctx, companyID)
	if err != nil {
		return ReindexResult{}, fmt.Errorf("resolve embedding client: %w", err)
	}
	if embClient == nil {
		return ReindexResult{}, fmt.Errorf("no embedding credentials configured for this company (set env defaults or per-tenant override)")
	}

	schema, err := s.schemaTool.FetchSchema(ctx, companyID, sourceID, true)
	if err != nil {
		return ReindexResult{}, fmt.Errorf("fetch schema: %w", err)
	}
	if schema == nil || len(schema.Tables) == 0 {
		return ReindexResult{}, fmt.Errorf("source has no tables to index")
	}

	docs := make([]string, 0, len(schema.Tables))
	items := make([]domain.TableEmbedding, 0, len(schema.Tables))
	skipped := 0
	for _, t := range schema.Tables {
		if isNoiseTable(t.Name) {
			skipped++
			continue
		}
		doc := buildTableDoc(t)
		sum := sha256.Sum256([]byte(doc))
		docs = append(docs, doc)
		items = append(items, domain.TableEmbedding{
			SourceID:  sourceID,
			TableName: t.Name,
			DocText:   doc,
			DocHash:   hex.EncodeToString(sum[:]),
			Model:     embClient.Model(),
		})
	}
	if len(items) == 0 {
		return ReindexResult{}, fmt.Errorf("all %d tables matched noise filter; nothing to index", len(schema.Tables))
	}

	vecs, err := embClient.Embed(ctx, docs)
	if err != nil {
		return ReindexResult{}, fmt.Errorf("embed: %w", err)
	}
	if len(vecs) != len(items) {
		return ReindexResult{}, fmt.Errorf("embed returned %d vectors for %d tables", len(vecs), len(items))
	}
	for i := range items {
		items[i].Embedding = vecs[i]
	}

	if err := s.embRepo.DeleteBySource(ctx, sourceID); err != nil {
		return ReindexResult{}, fmt.Errorf("clear existing embeddings: %w", err)
	}
	if err := s.embRepo.UpsertBatch(ctx, companyID, items); err != nil {
		return ReindexResult{}, fmt.Errorf("upsert embeddings: %w", err)
	}

	indexedAt := time.Now()
	if s.connMarker != nil {
		if err := s.connMarker.MarkEmbeddingsIndexed(ctx, sourceID, indexedAt); err != nil {
			logrus.WithError(err).Warn("mark embeddings_indexed_at failed; embeddings still written")
		}
	}
	logrus.WithFields(logrus.Fields{
		"company_id":    companyID,
		"source_id":     sourceID,
		"indexed":       len(items),
		"skipped_noise": skipped,
		"total":         len(schema.Tables),
		"duration_ms":   time.Since(start).Milliseconds(),
	}).Info("embedding: reindex complete")
	return ReindexResult{
		Tables:    len(items),
		Skipped:   skipped,
		Duration:  time.Since(start),
		IndexedAt: indexedAt,
	}, nil
}

// TestRetrieval runs the full RAG path for one ad-hoc query without going
// through chat: embed the query, pull top-K from the table_embeddings index,
// and return the filtered schema the agent would see. Use it to sanity-check
// whether reindex + the picker are producing useful shortlists.
func (s *EmbeddingService) TestRetrieval(ctx context.Context, companyID, sourceID, query string, topK int) (*RetrievalResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	if topK <= 0 {
		topK = 8
	}

	conn, err := s.conns.GetByID(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if conn.CompanyID != companyID {
		return nil, domain.ErrUnauthorized
	}

	embClient, err := s.embedCache.For(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("resolve embedding client: %w", err)
	}
	if embClient == nil {
		return nil, fmt.Errorf("no embedding credentials configured for this company")
	}

	embedStart := time.Now()
	vecs, err := embClient.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embed query returned no vectors")
	}
	embedMs := time.Since(embedStart).Milliseconds()

	indexedCount, err := s.embRepo.CountBySource(ctx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("count embeddings: %w", err)
	}

	topkStart := time.Now()
	hits, err := s.embRepo.TopK(ctx, sourceID, vecs[0], topK)
	if err != nil {
		return nil, fmt.Errorf("topk: %w", err)
	}
	topkMs := time.Since(topkStart).Milliseconds()

	schemaStart := time.Now()
	schema, err := s.schemaTool.FetchSchema(ctx, companyID, sourceID, false)
	if err != nil {
		return nil, fmt.Errorf("fetch schema: %w", err)
	}
	schemaMs := time.Since(schemaStart).Milliseconds()

	wanted := make(map[string]struct{}, len(hits))
	retHits := make([]RetrievalHit, 0, len(hits))
	for _, h := range hits {
		wanted[strings.ToLower(h.TableName)] = struct{}{}
		retHits = append(retHits, RetrievalHit{Table: h.TableName, Distance: h.Distance})
	}
	filteredTables := make([]db.TableInfo, 0, len(hits))
	for _, t := range schema.Tables {
		if _, ok := wanted[strings.ToLower(t.Name)]; ok {
			filteredTables = append(filteredTables, t)
		}
	}
	filtered := &db.SchemaMetadata{
		DBType:      schema.DBType,
		ExtractedAt: schema.ExtractedAt,
		Tables:      filteredTables,
	}

	return &RetrievalResult{
		Query:            query,
		SourceID:         sourceID,
		TopK:             topK,
		Hits:             retHits,
		TotalTables:      len(schema.Tables),
		IndexedTables:    indexedCount,
		FilteredTables:   len(filteredTables),
		SchemaPreview:    db.FormatSchemaForPrompt(filtered),
		EmbedDurationMs:  embedMs,
		TopKDurationMs:   topkMs,
		SchemaDurationMs: schemaMs,
	}, nil
}

// buildTableDoc renders the input string we send to the embedding model.
// Format kept terse: name + description + a bounded column list. The
// retrieval target is "match user question to table topic", so we
// concatenate column names too — they carry semantic signal (e.g. a
// `customer_email` column makes the table look customer-ish).
func buildTableDoc(t db.TableInfo) string {
	var b strings.Builder
	b.WriteString("Table: ")
	b.WriteString(t.Name)
	b.WriteString("\nDescription: ")
	if t.Description != "" {
		b.WriteString(t.Description)
	} else {
		b.WriteString("(none)")
	}
	b.WriteString("\nColumns: ")
	limit := len(t.Columns)
	if limit > maxColumnsInDoc {
		limit = maxColumnsInDoc
	}
	for i := 0; i < limit; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		c := t.Columns[i]
		b.WriteString(c.Name)
		if c.Type != "" {
			b.WriteString(" (")
			b.WriteString(c.Type)
			b.WriteString(")")
		}
	}
	if len(t.Columns) > maxColumnsInDoc {
		fmt.Fprintf(&b, ", ... (%d more)", len(t.Columns)-maxColumnsInDoc)
	}
	return b.String()
}
