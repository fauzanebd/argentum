package domain

import "testing"

// The carousel is the second format the render service draws (T-G6), and the
// four predicates below are the four places a new format has to be admitted.
// Pinned as a table so the next one cannot be admitted in three of them.
func TestCarouselIsAnAsyncRenderedZip(t *testing.T) {
	f := DocumentFormatCarousel
	if !f.Valid() {
		t.Error("carousel is not a valid format")
	}
	if !f.Async() {
		t.Error("carousel is not async — a request would wait minutes for the render service")
	}
	if !f.Rendered() {
		t.Error("carousel is not rendered elsewhere")
	}
	if got := f.Extension(); got != "zip" {
		t.Errorf("extension = %q, want zip", got)
	}
	if got := f.ContentType(); got != "application/zip" {
		t.Errorf("content type = %q, want application/zip", got)
	}
	// And the in-process formats stay where they were: a PDF is neither.
	if DocumentFormatPDF.Async() || DocumentFormatPDF.Rendered() {
		t.Error("pdf became async or rendered")
	}
	if !DocumentFormatMP4.Async() || !DocumentFormatMP4.Rendered() {
		t.Error("mp4 lost its async/rendered predicates")
	}
}
