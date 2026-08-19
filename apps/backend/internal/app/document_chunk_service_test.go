package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/docchunk"
	"github.com/fauzanebd/argentum/internal/docparse"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/embedding"
)

type stubChunkRepo struct {
	stored  []*domain.DocumentChunk
	deleted bool
	lexical []*domain.DocumentChunkHit
	dense   []*domain.DocumentChunkHit
}

func (r *stubChunkRepo) ReplaceForDocument(_ context.Context, _, _ string, chunks []*domain.DocumentChunk) error {
	r.stored = chunks
	return nil
}

func (r *stubChunkRepo) SearchDense(_ context.Context, _, _ string, _ []float32, _ int) ([]*domain.DocumentChunkHit, error) {
	return r.dense, nil
}

func (r *stubChunkRepo) SearchLexical(_ context.Context, _, _, _ string, _ int) ([]*domain.DocumentChunkHit, error) {
	return r.lexical, nil
}

func (r *stubChunkRepo) CountForDocument(_ context.Context, _, _ string) (int, error) { return 0, nil }

func (r *stubChunkRepo) DeleteForDocument(_ context.Context, _, _ string) error {
	r.deleted = true
	return nil
}

// stubEmbedder is the resolver's two interesting shapes: a client, or the
// (nil, nil) a company with no credential gets — which is not an error and is
// exactly why nothing ever noticed.
type stubEmbedder struct {
	client embedding.Client
	err    error
}

func (e stubEmbedder) For(context.Context, string) (embedding.Client, error) {
	return e.client, e.err
}

type stubEmbedClient struct{ calls int }

func (c *stubEmbedClient) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	c.calls++
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = []float32{float32(i), 1, 0}
	}
	return out, nil
}
func (c *stubEmbedClient) Model() string { return "stub-embed-1" }
func (c *stubEmbedClient) Dim() int      { return 3 }

type stubLightLLM struct {
	calls  int
	reply  string
	err    error
	prompt string
}

func (l *stubLightLLM) Generate(_ context.Context, prompt string, _ ...interfaces.GenerateOption) (string, error) {
	l.calls++
	l.prompt = prompt
	return l.reply, l.err
}

func testDoc() *domain.SourceDocument {
	return &domain.SourceDocument{ID: "doc-1", CompanyID: "co-1", Filename: "laporan.pdf"}
}

func testPages() []docparse.Page {
	return []docparse.Page{{
		Number:   1,
		Kind:     docparse.KindText,
		Markdown: "Laporan penjualan kuartal keempat.\n\nAngka di bawah ini belum diaudit.",
	}}
}

// TestSynopsisIsNotBoughtWithoutAnEmbedder is the guard that makes
// DOC_CHUNK_SYNOPSIS honest: the prefix it generates is read by the embedder
// and by nothing else — `search_documents` returns the heading, the pages and
// the text, never the prefix — so a deployment with no embedding credential
// must not pay a light-model call per uploaded document to fill a column
// nobody selects.
func TestSynopsisIsNotBoughtWithoutAnEmbedder(t *testing.T) {
	repo := &stubChunkRepo{}
	llm := &stubLightLLM{reply: "Laporan penjualan Q4 2024."}
	svc := NewDocumentChunkService(repo, stubEmbedder{}, docchunkOptions(), 5).
		WithSynopsis(llm, "light-model")

	if err := svc.Ingest(context.Background(), testDoc(), testPages()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if llm.calls != 0 {
		t.Errorf("light-model calls = %d, want 0 when no embedding client resolves", llm.calls)
	}
	if len(repo.stored) == 0 {
		t.Fatal("no chunks stored")
	}
	for _, c := range repo.stored {
		if c.ContextPrefix != "" {
			t.Errorf("ContextPrefix = %q, want empty", c.ContextPrefix)
		}
		if len(c.Embedding) != 0 {
			t.Errorf("chunk carries an embedding with no client")
		}
	}
}

// TestSynopsisRunsWhenAnEmbedderResolves is the other half: where the prefix
// has a consumer, it is generated, stored, and lands in front of the text that
// is embedded.
func TestSynopsisRunsWhenAnEmbedderResolves(t *testing.T) {
	repo := &stubChunkRepo{}
	llm := &stubLightLLM{reply: "Laporan penjualan Q4 2024."}
	client := &stubEmbedClient{}
	svc := NewDocumentChunkService(repo, stubEmbedder{client: client}, docchunkOptions(), 5).
		WithSynopsis(llm, "light-model")

	if err := svc.Ingest(context.Background(), testDoc(), testPages()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if llm.calls != 1 {
		t.Fatalf("light-model calls = %d, want 1 per document", llm.calls)
	}
	if !strings.Contains(llm.prompt, "laporan.pdf") {
		t.Error("the synopsis prompt does not name the document")
	}
	if client.calls != 1 {
		t.Errorf("embed calls = %d, want 1 batch", client.calls)
	}
	if len(repo.stored) == 0 {
		t.Fatal("no chunks stored")
	}
	for _, c := range repo.stored {
		if c.ContextPrefix != "Laporan penjualan Q4 2024." {
			t.Errorf("ContextPrefix = %q", c.ContextPrefix)
		}
		if len(c.Embedding) == 0 || c.Model != "stub-embed-1" {
			t.Errorf("chunk %d was not embedded: %+v", c.Ordinal, c)
		}
	}
	if got := embeddableText(repo.stored[0]); !strings.HasPrefix(got, "Laporan penjualan Q4 2024.\n") {
		t.Errorf("embeddable text does not open with the prefix: %.60q", got)
	}
}

// TestWithSynopsisNilLeavesItOff: passing no client is how the caller says
// DOC_CHUNK_SYNOPSIS=false.
func TestWithSynopsisNilLeavesItOff(t *testing.T) {
	repo := &stubChunkRepo{}
	client := &stubEmbedClient{}
	svc := NewDocumentChunkService(repo, stubEmbedder{client: client}, docchunkOptions(), 5).
		WithSynopsis(nil, "light-model")

	if err := svc.Ingest(context.Background(), testDoc(), testPages()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	for _, c := range repo.stored {
		if c.ContextPrefix != "" {
			t.Errorf("ContextPrefix = %q, want empty", c.ContextPrefix)
		}
	}
}

// TestIngestDegradesWhenTheResolverErrors: an embedding provider that is down
// stores chunks for the lexical index rather than refusing the upload, and buys
// no synopsis on the way past.
func TestIngestDegradesWhenTheResolverErrors(t *testing.T) {
	repo := &stubChunkRepo{}
	llm := &stubLightLLM{reply: "tidak dipakai"}
	svc := NewDocumentChunkService(repo, stubEmbedder{err: errors.New("provider down")}, docchunkOptions(), 5).
		WithSynopsis(llm, "light-model")

	if err := svc.Ingest(context.Background(), testDoc(), testPages()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(repo.stored) == 0 {
		t.Fatal("the upload was refused instead of degrading")
	}
	if llm.calls != 0 {
		t.Errorf("light-model calls = %d, want 0 when the embedder errored", llm.calls)
	}
}

// TestIngestOfAScanClearsPreviousChunks: a document whose pages hold no text at
// all removes what an earlier parse left, rather than leaving prose behind that
// describes text this build cannot see.
func TestIngestOfAnUnreadableDocumentClearsPreviousChunks(t *testing.T) {
	repo := &stubChunkRepo{}
	svc := NewDocumentChunkService(repo, stubEmbedder{}, docchunkOptions(), 5)

	pages := []docparse.Page{{Number: 1, Kind: docparse.KindNeedsOCR}}
	if err := svc.Ingest(context.Background(), testDoc(), pages); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !repo.deleted {
		t.Error("previous chunks were not cleared")
	}
}

// TestSynopsisFailureIsNotAnIngestFailure: the prefix is a quality feature, so
// a light model that errors costs the prefix and nothing else.
func TestSynopsisFailureIsNotAnIngestFailure(t *testing.T) {
	repo := &stubChunkRepo{}
	llm := &stubLightLLM{err: errors.New("model unavailable")}
	svc := NewDocumentChunkService(repo, stubEmbedder{client: &stubEmbedClient{}}, docchunkOptions(), 5).
		WithSynopsis(llm, "light-model")

	if err := svc.Ingest(context.Background(), testDoc(), testPages()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(repo.stored) == 0 {
		t.Fatal("no chunks stored")
	}
	if repo.stored[0].ContextPrefix != "" {
		t.Errorf("ContextPrefix = %q, want empty after a failed synopsis", repo.stored[0].ContextPrefix)
	}
}

// docchunkOptions is the shape the worker builds: a small budget so a fixture
// stays one chunk, and no heading detection, which is the shipped default.
func docchunkOptions() docchunk.Options {
	return docchunk.Options{MaxTokens: 500, Overlap: 60}
}
