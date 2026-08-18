package docparse

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseReadsADocument(t *testing.T) {
	var gotSecret, gotMaxPages, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecret = r.Header.Get("x-docparse-secret")
		gotMaxPages = r.URL.Query().Get("max_pages")
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"page_count": 2,
			"parser": {"name": "pdfplumber", "version": "0.11.4"},
			"pages": [
				{"page_no":1,"kind":"text","char_count":812,"alnum_ratio":0.98,"markdown":"Laporan",
				 "tables":[{"index":0,"strategy":"lines","rows":[["Region","Revenue"],["Jakarta","3.863.405.700"]],"row_count":2,"col_count":2}]},
				{"page_no":2,"kind":"needs_ocr","char_count":3,"alnum_ratio":1.0,"image_area_ratio":0.97}
			]}`))
	}))
	defer srv.Close()

	p := New(Options{BaseURL: srv.URL, Secret: "s3cret", Timeout: 5 * time.Second})
	doc, err := p.Parse(context.Background(), strings.NewReader("%PDF-1.7"), 200)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if gotSecret != "s3cret" || gotMaxPages != "200" || gotContentType != "application/pdf" {
		t.Errorf("request = secret %q, max_pages %q, type %q", gotSecret, gotMaxPages, gotContentType)
	}
	if doc.PageCount != 2 || len(doc.Pages) != 2 {
		t.Fatalf("page count = %d with %d pages", doc.PageCount, len(doc.Pages))
	}
	if doc.TextPages() != 1 || doc.NeedsOCRPages() != 1 {
		t.Errorf("text=%d needs_ocr=%d, want 1 and 1", doc.TextPages(), doc.NeedsOCRPages())
	}
	if doc.Parser.Version != "0.11.4" {
		t.Errorf("parser version = %q; T-P13 reads this off every report", doc.Parser.Version)
	}
	if got := doc.Pages[0].Tables[0].Strategy; got != "lines" {
		t.Errorf("table strategy = %q, want lines", got)
	}
}

// A page limit is terminal: the document will have the same page count on the
// retry, so this must not come back as something a queue keeps trying.
func TestPageLimitIsARefusalWithTheNumbersInIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"page_limit","page_count":412,"max_pages":200}`))
	}))
	defer srv.Close()

	_, err := New(Options{BaseURL: srv.URL}).Parse(context.Background(), strings.NewReader("%PDF-"), 200)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
	// The sentence ends up on a row a person reads, so the counts have to be in
	// it — "the parser refused this document" tells them nothing they can act on.
	if !strings.Contains(err.Error(), "412") || !strings.Contains(err.Error(), "200") {
		t.Errorf("message %q carries neither the count nor the limit", err.Error())
	}
}

func TestUnreadableFileIsARefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"unreadable","detail":"No /Root object"}`))
	}))
	defer srv.Close()

	_, err := New(Options{BaseURL: srv.URL}).Parse(context.Background(), strings.NewReader("%PDF-"), 0)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
	if !strings.Contains(err.Error(), "No /Root object") {
		t.Errorf("message %q dropped the parser's own reason", err.Error())
	}
}

// A wrong shared secret is a configuration error. Reporting it as unreachable
// sends an operator to look at the network for a problem in an env var.
func TestBadSecretIsNotUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := New(Options{BaseURL: srv.URL}).Parse(context.Background(), strings.NewReader("%PDF-"), 0)
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Error("a bad secret was reported as an unreachable service")
	}
}

func TestServerErrorIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := New(Options{BaseURL: srv.URL}).Parse(context.Background(), strings.NewReader("%PDF-"), 0)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestUnreachableServiceIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	_, err := New(Options{BaseURL: url, Timeout: time.Second}).Parse(
		context.Background(), strings.NewReader("%PDF-"), 0)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// A 200 carrying no pages is a broken answer, not an empty document. Writing
// 'parsed' on it would tell a tenant their file holds nothing.
func TestEmptyAnswerIsNotAnEmptyDocument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"page_count":0,"pages":[]}`))
	}))
	defer srv.Close()

	_, err := New(Options{BaseURL: srv.URL}).Parse(context.Background(), strings.NewReader("%PDF-"), 0)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// No base URL is a supported deployment, and it must be one call away from
// saying so rather than a nil dereference at the first parse.
func TestNoBaseURLYieldsANilParserThatRefusesPolitely(t *testing.T) {
	p := New(Options{})
	if p != nil {
		t.Fatal("a parser was built without a base URL")
	}
	_, err := p.Parse(context.Background(), strings.NewReader("%PDF-"), 0)
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}
