package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/docchunk"
	"github.com/fauzanebd/argentum/internal/docparse"
	"github.com/fauzanebd/argentum/internal/domain"
)

// DocumentChunkService turns a parsed document's prose into retrievable chunks,
// and retrieves them (T-P8).
//
// **Both halves of retrieval, merged by rank.** Dense vectors find the
// paragraph that means the same thing in different words; the lexical index
// finds the clause number, the product code and the party's name — which is
// most of what a contract is made of and exactly what an embedding is worst at.
// Neither is a superset of the other, so the merge is the feature and either
// half alone is the degraded mode.
//
// **A deployment with no embeddings still works.** Ingest stores chunks with a
// NULL embedding, the dense half returns nothing, and the lexical half answers
// on its own. The alternative — refusing to ingest without embedding
// credentials — would make a text-search feature depend on a model provider.
type DocumentChunkService struct {
	chunks domain.DocumentChunkRepository
	// embed is the tenant's embedding client, or nil. Resolved per company for
	// the reason the cookbook resolves it per company: the credentials are the
	// tenant's, and a shared client would spend one tenant's key on another's
	// document.
	embed EmbeddingResolver
	// llm writes the one-sentence synopsis every chunk of a document is
	// prefixed with. Nil leaves the prefix empty, which costs retrieval quality
	// and nothing else. Set by [DocumentChunkService.WithSynopsis], from
	// `DOC_CHUNK_SYNOPSIS`.
	llm   LightLLM
	model string
	opts  docchunk.Options
	topK  int
}

// LightLLM is the one-shot generation this service needs. Same shape as
// ConnectionDescriber's, and named separately so a change to either does not
// silently change the other.
type LightLLM interface {
	Generate(ctx context.Context, prompt string, opts ...interfaces.GenerateOption) (string, error)
}

func NewDocumentChunkService(
	chunks domain.DocumentChunkRepository, embed EmbeddingResolver, opts docchunk.Options, topK int,
) *DocumentChunkService {
	if topK <= 0 {
		topK = 5
	}
	return &DocumentChunkService{chunks: chunks, embed: embed, opts: opts, topK: topK}
}

// WithSynopsis turns on the generated context prefix. Passing a nil client
// leaves it off.
//
// Called from `bootstrap.Stack` under `DOC_CHUNK_SYNOPSIS`. It was called from
// nowhere at all between T-P8 and 2026-08-19, while that setting defaulted to
// true — so the prefix this service documents at length was empty on every
// document any deployment had ingested, and the argument for it had never been
// tested against anything.
func (s *DocumentChunkService) WithSynopsis(llm LightLLM, model string) *DocumentChunkService {
	if llm == nil {
		return s
	}
	s.llm = llm
	s.model = model
	return s
}

// Ingest chunks a parsed document and stores it, replacing whatever was there.
//
// Called from the parse worker with the pages it already holds, rather than
// re-reading the artifacts: the parse has the whole document in memory at
// exactly this moment, and a second pass over object storage would buy nothing
// but a window where the chunks describe a previous parse.
func (s *DocumentChunkService) Ingest(ctx context.Context, doc *domain.SourceDocument, pages []docparse.Page) error {
	if s == nil || s.chunks == nil {
		return nil
	}
	built := docchunk.Build(pages, s.opts)
	if len(built) == 0 {
		// A document whose pages are all scans. Its previous chunks — if a
		// better parse once produced some — are cleared rather than left
		// behind, because they would describe text this build cannot see.
		return s.chunks.DeleteForDocument(ctx, doc.CompanyID, doc.ID)
	}

	// Resolved before the synopsis rather than after it, because the synopsis is
	// only ever read by the embedder. `context_prefix` is stored, returned to
	// `search_documents` in the hit — and dropped there: the tool's passage
	// carries the heading, the pages and the text, never the prefix. So on a
	// deployment where no embedding client resolves, generating one is a
	// light-model call per uploaded document whose entire effect is a column
	// nobody selects.
	client, embedErr := s.embedder(ctx, doc.CompanyID)
	if embedErr != nil {
		// Degrade rather than fail. The lexical half is a complete answer, and
		// a document that would not ingest because an embedding provider was
		// down is a document a tenant has to remember to upload again.
		logrus.WithError(embedErr).WithField("document_id", doc.ID).
			Warn("chunks stored without embeddings; document search will use the lexical index only")
	}

	var prefix string
	if client != nil {
		prefix = s.synopsis(ctx, doc, built)
	}
	rows := make([]*domain.DocumentChunk, 0, len(built))
	texts := make([]string, 0, len(built))
	// The filename's search terms, computed once and copied onto every chunk
	// (T-P14). It is stored rather than joined because the chunk's `tsv` is a
	// generated column and cannot read another table's row; a rename that goes
	// through the upload path re-ingests, which is what keeps the copy true.
	sourceName := domain.FilenameSearchTerms(doc.Filename)
	for _, c := range built {
		row := &domain.DocumentChunk{
			DocumentID:    doc.ID,
			CompanyID:     doc.CompanyID,
			Ordinal:       c.Ordinal,
			PageFrom:      c.PageFrom,
			PageTo:        c.PageTo,
			HeadingPath:   c.HeadingPath,
			Content:       c.Content,
			ContextPrefix: prefix,
			SourceName:    sourceName,
		}
		rows = append(rows, row)
		texts = append(texts, embeddableText(row))
	}

	if client != nil {
		vectors, err := client.Embed(ctx, texts)
		if err != nil {
			logrus.WithError(err).WithField("document_id", doc.ID).
				Warn("embedding the document failed; storing chunks for lexical search only")
		} else {
			for i := range rows {
				if i < len(vectors) {
					rows[i].Embedding = vectors[i]
					rows[i].Model = client.Model()
				}
			}
		}
	}

	if err := s.chunks.ReplaceForDocument(ctx, doc.CompanyID, doc.ID, rows); err != nil {
		return fmt.Errorf("store document chunks: %w", err)
	}
	embedded := 0
	for _, r := range rows {
		if len(r.Embedding) > 0 {
			embedded++
		}
	}
	logrus.WithFields(logrus.Fields{
		"company_id": doc.CompanyID, "document_id": doc.ID,
		"chunks": len(rows), "embedded": embedded, "synopsis": prefix != "",
	}).Info("document chunked")
	return nil
}

// Search is the hybrid retrieval one tool call runs.
//
// The bool is `loosened` (T-P14): the lexical half found nothing with every
// term required and answered with a disjunctive re-run. The caller is expected
// to say so — a passage retrieved by four of a question's five terms is a
// weaker answer than one retrieved by all five, and only the tool result can
// carry that difference to the model.
func (s *DocumentChunkService) Search(
	ctx context.Context, companyID, documentID, query string, topK int,
) ([]*domain.DocumentChunkHit, bool, error) {
	if s == nil || s.chunks == nil {
		return nil, false, fmt.Errorf("document search is not configured on this deployment")
	}
	if strings.TrimSpace(query) == "" {
		return nil, false, fmt.Errorf("%w: a search needs a query", domain.ErrInvalidInput)
	}
	if topK <= 0 {
		topK = s.topK
	}

	// Each half is asked for more than the caller wants, because the merge
	// below rewards a chunk both halves found and a top-5 from each is a
	// thinner overlap than a top-15.
	depth := topK * 3
	lexical, loosened, err := s.chunks.SearchLexical(ctx, companyID, documentID, query, depth)
	if err != nil {
		return nil, false, err
	}

	var dense []*domain.DocumentChunkHit
	if client, err := s.embedder(ctx, companyID); err == nil && client != nil {
		if vectors, err := client.Embed(ctx, []string{query}); err == nil && len(vectors) > 0 {
			dense, err = s.chunks.SearchDense(ctx, companyID, documentID, vectors[0], depth)
			if err != nil {
				return nil, false, err
			}
		} else if err != nil {
			logrus.WithError(err).Warn("embedding the search query failed; answering from the lexical index")
		}
	}

	merged := fuse(dense, lexical, topK)
	// Reported only where the loosened half is what the caller is reading. A
	// dense index that answered the same question exactly makes the fallback an
	// implementation detail rather than a caveat worth spending the model's
	// attention on.
	return merged, loosened && !onlyDense(merged), nil
}

// onlyDense reports whether every passage in the merged list came from the
// vector half — the case where the lexical fallback contributed nothing that
// survived fusion, so there is no loosened match for the caller to caveat.
func onlyDense(hits []*domain.DocumentChunkHit) bool {
	for _, h := range hits {
		if h.Matched != "dense" {
			return false
		}
	}
	return true
}

// fuse merges the two result lists by reciprocal rank.
//
// Reciprocal rank rather than a weighted score, because the two halves are on
// incomparable scales — a cosine distance and a `ts_rank` cannot be added, and
// any constant that made them comparable today would be wrong the first time
// either index changed. What RRF encodes instead is the only claim that
// survives that: a chunk both halves ranked highly is better than one either
// ranked highest.
func fuse(dense, lexical []*domain.DocumentChunkHit, topK int) []*domain.DocumentChunkHit {
	const k = 60.0 // the published constant; it flattens the head of each list

	type entry struct {
		hit   *domain.DocumentChunkHit
		score float64
		in    map[string]bool
	}
	merged := map[int64]*entry{}
	add := func(hits []*domain.DocumentChunkHit, half string) {
		for rank, hit := range hits {
			e, ok := merged[hit.ID]
			if !ok {
				e = &entry{hit: hit, in: map[string]bool{}}
				merged[hit.ID] = e
			}
			e.score += 1 / (k + float64(rank+1))
			e.in[half] = true
		}
	}
	add(dense, "dense")
	add(lexical, "lexical")

	out := make([]*domain.DocumentChunkHit, 0, len(merged))
	for _, e := range merged {
		e.hit.Score = e.score
		switch {
		case e.in["dense"] && e.in["lexical"]:
			e.hit.Matched = "both"
		case e.in["dense"]:
			e.hit.Matched = "dense"
		default:
			e.hit.Matched = "lexical"
		}
		out = append(out, e.hit)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		// A deterministic tiebreak, so two runs of the same query return the
		// same order. Map iteration would not, and a retrieval order that
		// changes between identical runs makes every eval score noise.
		return out[i].ID < out[j].ID
	})
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

// synopsis writes one sentence situating this document, prepended to every
// chunk of it before embedding.
//
// **This is a deliberate departure from T-P8, and it is a cost decision.** The
// ticket describes contextual retrieval as published: one generated sentence
// *per chunk*, situating that chunk in its document. That is one light-model
// call per chunk — eighty on a forty-page report — and the published measurement
// was taken with prompt caching this deployment's light tier does not
// necessarily have. What is built instead is one call per *document*, whose
// sentence goes on every chunk, with the per-chunk half of the context supplied
// by `heading_path`, which is free and exact.
//
// The trade is written down rather than hidden because it is measurable: T-P13's
// answer-correctness score is where a per-chunk prefix would prove itself, and
// the day it does, this method is where it goes.
//
// **Called only where an embedding client resolved**, because the prefix is
// embedded and read nowhere else. That guard is what keeps `DOC_CHUNK_SYNOPSIS`
// from being a per-document model call bought by a deployment that has no dense
// index to spend it on.
func (s *DocumentChunkService) synopsis(ctx context.Context, doc *domain.SourceDocument, chunks []docchunk.Chunk) string {
	if s.llm == nil || len(chunks) == 0 {
		return ""
	}
	var sample strings.Builder
	for _, c := range chunks {
		if sample.Len() > 2000 {
			break
		}
		sample.WriteString(c.Content)
		sample.WriteString("\n\n")
	}
	prompt := "Document filename: " + doc.Filename + "\n\nOpening content:\n" +
		sample.String() + "\n\nIn one sentence of at most 25 words, say what this document is " +
		"and what period or subject it covers. Answer in the document's own language. " +
		"Do not quote any figure from it."
	out, err := s.llm.Generate(ctx, prompt,
		interfaces.WithSystemMessage(
			"You write one-line descriptions of business documents so they can be found again. "+
				"You never state numbers, and you never guess at content you were not shown."),
		interfaces.WithTemperature(0.2),
	)
	if err != nil {
		logrus.WithError(err).WithField("document_id", doc.ID).
			Warn("document synopsis failed; chunks are stored without a context prefix")
		return ""
	}
	out = strings.TrimSpace(strings.ReplaceAll(out, "\n", " "))
	// Bounded, because it is prepended to every chunk before embedding: a model
	// that answers with three paragraphs would push the chunk's own text out of
	// the embedding window and make every chunk of the document look alike.
	return domain.ClampRunes(out, 200)
}

func (s *DocumentChunkService) embedder(ctx context.Context, companyID string) (embeddingClient, error) {
	if s.embed == nil {
		return nil, nil
	}
	client, err := s.embed.For(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, nil
	}
	return client, nil
}

// embeddingClient is the shape both callers use, narrowed to what this service
// touches so a test double is three methods rather than a package.
type embeddingClient interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
	Model() string
	Dim() int
}

// embeddableText is what actually gets embedded: the synopsis, the heading
// trail, then the chunk. The prefixes are cheap and they are what make two
// paragraphs with the same words in different sections retrievable apart.
func embeddableText(c *domain.DocumentChunk) string {
	var b strings.Builder
	if c.ContextPrefix != "" {
		b.WriteString(c.ContextPrefix)
		b.WriteString("\n")
	}
	if c.HeadingPath != "" {
		b.WriteString(c.HeadingPath)
		b.WriteString("\n")
	}
	b.WriteString(c.Content)
	return b.String()
}
