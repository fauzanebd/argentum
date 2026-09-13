package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// T-Z6: search_documents quotes, for the person on a turn, only passages from
// documents they may read — "because a passage quoted into an answer is a read"
// — and the turn does not learn that anything was withheld.

// documentReaders is authz for documents, with a record of what it was asked.
type documentReaders struct {
	hidden  map[string]bool
	err     error
	decided []string
	listed  [][]string
}

func (g *documentReaders) Decide(_ context.Context, _ authz.Subject, kind domain.ResourceKind, id string) (authz.Decision, error) {
	g.decided = append(g.decided, id)
	if kind != domain.ResourceKindDocument {
		return authz.Decision{}, fmt.Errorf("asked about %s, not documents", kind)
	}
	if g.err != nil {
		return authz.Decision{}, g.err
	}
	if g.hidden[id] {
		return authz.Decision{Reason: authz.ReasonNotGranted}, nil
	}
	return authz.Decision{Allowed: true, Reason: authz.ReasonOpen}, nil
}

func (g *documentReaders) Visible(_ context.Context, _ authz.Subject, kind domain.ResourceKind, ids []string) ([]string, error) {
	g.listed = append(g.listed, slices.Clone(ids))
	if kind != domain.ResourceKindDocument {
		return nil, fmt.Errorf("asked about %s, not documents", kind)
	}
	if g.err != nil {
		return nil, g.err
	}
	out := []string{}
	for _, id := range ids {
		if !g.hidden[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func (g *documentReaders) asked() int { return len(g.decided) + len(g.listed) }

// rankedSearch answers a company-wide search from one ranking cut to topK, and a
// one-document search from that document's own passages. It records every call.
type rankedSearch struct {
	ranking []*domain.DocumentChunkHit
	calls   []string // "document|topK"
}

func (s *rankedSearch) Search(_ context.Context, _, documentID, _ string, topK int) ([]*domain.DocumentChunkHit, bool, error) {
	s.calls = append(s.calls, fmt.Sprintf("%s|%d", documentID, topK))
	if topK <= 0 {
		topK = 5
	}
	var out []*domain.DocumentChunkHit
	for _, h := range s.ranking {
		if documentID == "" || h.DocumentID == documentID {
			out = append(out, h)
		}
	}
	return out[:min(topK, len(out))], false, nil
}

func chunkOf(documentID, filename, text string) *domain.DocumentChunkHit {
	return &domain.DocumentChunkHit{
		DocumentChunk: domain.DocumentChunk{DocumentID: documentID, PageFrom: 1, PageTo: 1, Content: text},
		Filename:      filename,
		Matched:       "lexical",
	}
}

// payrollFirst ranks Payroll's salary passages above the store SOP's, which is
// the shape where a hidden document would crowd out every readable passage.
func payrollFirst() []*domain.DocumentChunkHit {
	return []*domain.DocumentChunkHit{
		chunkOf("doc-pay", "Payroll 2026.pdf", "Gaji pokok direktur Rp 85.000.000"),
		chunkOf("doc-sop", "Store SOP v3.pdf", "Gaji dibayar setiap tanggal 25"),
		chunkOf("doc-pay", "Payroll 2026.pdf", "Tunjangan jabatan manajer Rp 12.000.000"),
		chunkOf("doc-sop", "Store SOP v3.pdf", "Lembur dihitung per jam"),
		chunkOf("doc-sop", "Store SOP v3.pdf", "Cuti tahunan 12 hari"),
		chunkOf("doc-sop", "Store SOP v3.pdf", "Seragam diganti setiap tahun"),
	}
}

func searchFor(search DocumentSearch, access *documentReaders, userID, args string) (string, error) {
	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")
	if userID != "" {
		ctx = tenantctx.WithUserID(ctx, userID)
	}
	tool := NewSearchDocumentsTool(search)
	if access != nil {
		tool = tool.WithAccess(access)
	}
	return tool.Execute(ctx, args)
}

func passageDocuments(t *testing.T, out string) []string {
	t.Helper()
	var body struct {
		Passages []struct {
			DocumentID string `json:"document_id"`
		} `json:"passages"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("result is not JSON: %v: %s", err, out)
	}
	ids := make([]string, 0, len(body.Passages))
	for _, p := range body.Passages {
		ids = append(ids, p.DocumentID)
	}
	return ids
}

// "The turn does not reveal that it exists": asked about Payroll by id, the
// answer is byte-for-byte the answer about an id that is not a document at all.
func TestARestrictedDocumentNamedByIDReadsAsOneThatDoesNotExist(t *testing.T) {
	search := &rankedSearch{ranking: payrollFirst()}
	grants := &documentReaders{hidden: map[string]bool{"doc-pay": true}}
	refused, err := searchFor(search, grants, "u-budi", `{"query":"gaji direktur","document_id":"doc-pay"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(search.calls) != 0 {
		t.Errorf("searched %v for a document the person may not read; want nothing searched", search.calls)
	}

	missing, err := searchFor(&rankedSearch{ranking: payrollFirst()}, nil, "u-budi", `{"query":"gaji direktur","document_id":"doc-nope"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if refused != missing {
		t.Errorf("a restricted document answered\n%s\nand a missing one\n%s\nwant them identical", refused, missing)
	}
}

// The passages a hidden document crowded out take its places: asked for three,
// the person gets three readable ones, not one.
func TestPassagesFromADocumentThePersonMayNotReadAreLeftOut(t *testing.T) {
	search := &rankedSearch{ranking: payrollFirst()}
	grants := &documentReaders{hidden: map[string]bool{"doc-pay": true}}
	out, err := searchFor(search, grants, "u-budi", `{"query":"gaji","top_k":3}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if docs := passageDocuments(t, out); !slices.Equal(docs, []string{"doc-sop", "doc-sop", "doc-sop"}) {
		t.Errorf("passages from %v, want three from the SOP", docs)
	}
	if strings.Contains(out, "Payroll") || strings.Contains(out, "85.000.000") {
		t.Error("the result carried a word of the hidden document")
	}
	if !slices.Equal(search.calls, []string{"|3", "|12"}) {
		t.Errorf("searches %v, want the asked-for search and one four times as deep", search.calls)
	}
}

func TestWithNothingHiddenASearchIsExactlyAsBefore(t *testing.T) {
	grants := &documentReaders{}
	search := &rankedSearch{ranking: payrollFirst()}
	withAccess, err := searchFor(search, grants, "u-rina", `{"query":"gaji"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	without, err := searchFor(&rankedSearch{ranking: payrollFirst()}, nil, "u-rina", `{"query":"gaji"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if withAccess != without {
		t.Errorf("with nothing hidden the result changed:\n%s\nwant\n%s", withAccess, without)
	}
	if len(search.calls) != 1 || len(grants.listed) != 1 || len(grants.decided) != 0 {
		t.Errorf("%d searches, %v listed, %v decided; want one search and one question", len(search.calls), grants.listed, grants.decided)
	}
}

// Decision 7, asserted rather than assumed: a channel, a key, the widget and a
// watcher carry no person, and search every document as before.
func TestATurnWithNoPersonSearchesEveryDocument(t *testing.T) {
	grants := &documentReaders{hidden: map[string]bool{"doc-pay": true}}
	out, err := searchFor(&rankedSearch{ranking: payrollFirst()}, grants, "", `{"query":"gaji","top_k":3}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if docs := passageDocuments(t, out); !slices.Contains(docs, "doc-pay") || grants.asked() != 0 {
		t.Errorf("passages from %v after %d questions; want Payroll's included and nothing asked", docs, grants.asked())
	}
}

func TestADocumentCheckThatFailsSearchesNothing(t *testing.T) {
	for _, args := range []string{`{"query":"gaji","document_id":"doc-pay"}`, `{"query":"gaji"}`} {
		search := &rankedSearch{ranking: payrollFirst()}
		out, err := searchFor(search, &documentReaders{err: errors.New("control DB down")}, "u-budi", args)
		if err == nil || out != "" {
			t.Fatalf("%s: (%q, %v), want an error and no result", args, out, err)
		}
		if strings.Contains(err.Error(), "control DB") {
			t.Errorf("%s: the storage error reached the model: %v", args, err)
		}
		if strings.Contains(args, "document_id") && len(search.calls) != 0 {
			t.Errorf("%s: searched %v on a check that could not be made", args, search.calls)
		}
	}
}
