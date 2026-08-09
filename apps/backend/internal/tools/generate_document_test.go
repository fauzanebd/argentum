package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

type memStore struct{ objects map[string][]byte }

func (s *memStore) UploadKey(_ context.Context, key string, r io.Reader, _ string) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	s.objects[key] = data
	return "http://store.invalid/" + key, nil
}

func (s *memStore) PresignKey(_ context.Context, key string, _ time.Duration) (string, error) {
	return "http://store.invalid/" + key + "?signed", nil
}

type memDocs struct{ rows []*domain.Document }

func (d *memDocs) Insert(_ context.Context, doc *domain.Document) error {
	doc.CreatedAt = time.Unix(1_800_000_000, 0)
	cp := *doc
	d.rows = append(d.rows, &cp)
	return nil
}
func (d *memDocs) GetByID(context.Context, string) (*domain.Document, error) {
	return nil, domain.ErrNotFound
}
func (d *memDocs) GetForCompany(context.Context, string, string) (*domain.Document, error) {
	return nil, domain.ErrNotFound
}
func (d *memDocs) ListByCompany(context.Context, string, domain.DocumentFilter) ([]*domain.Document, bool, error) {
	return nil, false, nil
}
func (d *memDocs) ListByThread(context.Context, string) ([]*domain.Document, error) { return nil, nil }
func (d *memDocs) NewestForThreadSince(context.Context, string, string, time.Time) (*domain.Document, error) {
	return nil, domain.ErrNotFound
}

const csvArgs = `{"format":"csv","title":"t","content":{"table":{"columns":["A"],"rows":[["1"]]}}}`

func newTool() (*GenerateDocumentTool, *memDocs) {
	docs := &memDocs{}
	svc := docgen.New(&memStore{objects: map[string][]byte{}}, docs, nil, nil, nil, time.Hour)
	return NewGenerateDocumentTool(svc), docs
}

// A turn that arrived over `POST /v1/reports` produces an API document, and the
// tool learns that from the context rather than from a parameter — the caller
// is the model, four packages and a queue away from the HTTP request. The
// actor is already there for T-05's audit rows, so the audit log and the
// document row now agree about who produced it.
func TestGeneratedDocumentTakesItsProvenanceFromTheActor(t *testing.T) {
	tool, docs := newTool()

	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")
	ctx = tenantctx.WithThreadID(ctx, "th-1")
	ctx = tenantctx.WithActor(ctx, string(domain.ActorKindAPIKey), "key-9")

	if _, err := tool.Execute(ctx, csvArgs); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := docs.rows[0]
	if got.Source != domain.DocumentSourceAPI {
		t.Errorf("source = %q, want api", got.Source)
	}
	if got.APIKeyID != "key-9" {
		t.Errorf("api_key_id = %q, want key-9", got.APIKeyID)
	}
	// The thread is the point of the agentic door: a real turn ran on an
	// api-channel thread, and the document belongs to it.
	if got.ThreadID != "th-1" {
		t.Errorf("thread_id = %q, want th-1", got.ThreadID)
	}
}

// A person in the dashboard produces an agent document with no credential on
// it, exactly as before T-A2.
func TestGeneratedDocumentFromAPersonIsUnchanged(t *testing.T) {
	tool, docs := newTool()

	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")
	ctx = tenantctx.WithThreadID(ctx, "th-1")
	ctx = tenantctx.WithActor(ctx, string(domain.ActorKindUser), "user-3")

	if _, err := tool.Execute(ctx, csvArgs); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := docs.rows[0]
	if got.Source != domain.DocumentSourceAgent {
		t.Errorf("source = %q, want agent", got.Source)
	}
	if got.APIKeyID != "" {
		t.Errorf("api_key_id = %q, want empty for a person", got.APIKeyID)
	}
}

// The thread requirement stayed on the agent path when the rest moved into
// docgen: a turn without one is a bug in the worker's wiring and should fail
// loudly, while `/v1`'s render door legitimately has none.
func TestGenerateDocumentStillRequiresAThread(t *testing.T) {
	tool, _ := newTool()

	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")
	if _, err := tool.Execute(ctx, csvArgs); err == nil {
		t.Fatal("Execute accepted a turn with no thread")
	}
	if _, err := tool.Execute(context.Background(), csvArgs); err == nil {
		t.Fatal("Execute accepted a turn with no tenant")
	}
}

// The JSON the agent reads back is unchanged by the refactor: it is the tool's
// contract with the model, and the model was tuned against it.
func TestGenerateDocumentReturnsTheAgentsShape(t *testing.T) {
	tool, _ := newTool()
	ctx := tenantctx.WithThreadID(tenantctx.WithCompanyID(context.Background(), "co-1"), "th-1")

	out, err := tool.Execute(ctx, csvArgs)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("tool output is not JSON: %v", err)
	}
	for _, k := range []string{"document_id", "format", "filename", "download_url", "expires_at", "size_bytes", "note"} {
		if _, ok := body[k]; !ok {
			t.Errorf("tool output lost %q", k)
		}
	}
}

func (s *memStore) DownloadKey(_ context.Context, key string) ([]byte, error) {
	data, ok := s.objects[key]
	if !ok {
		return nil, errors.New("no such key")
	}
	return data, nil
}
