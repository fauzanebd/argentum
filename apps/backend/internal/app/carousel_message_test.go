package app

import (
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
)

// The announcement a finished carousel becomes (T-G6, decision 6): images on
// the authenticated page route, the zip presigned, the caption copyable.
func TestCarouselMessageNeverPresignsAnImage(t *testing.T) {
	res := &docgen.Result{
		Document:    &domain.Document{ID: "doc-1", Filename: "carousel.zip", Format: domain.DocumentFormatCarousel, PageCount: 3},
		DownloadURL: "http://store.invalid/documents/co/th/doc-1.zip?X-Amz-Signature=abc",
		Carousel: &docgen.CarouselManifest{
			Caption:  "Agustus tumbuh 9,8%",
			Hashtags: []string{"promo", "agustus"},
			Alts:     []string{"Penjualan Agustus", "Sorotan. Total Pendapatan: Rp 412 Juta", "Penjualan [Agustus] per Kanal"},
			Pages:    3,
		},
	}
	msg := carouselMessage(res)

	for i := 1; i <= 3; i++ {
		if !strings.Contains(msg, "](/api/documents/doc-1/pages/"+string(rune('0'+i))+")") {
			t.Errorf("no image for page %d in:\n%s", i, msg)
		}
	}
	if strings.Contains(msg, "![") && strings.Contains(msg, "X-Amz-Signature=abc)") && strings.Contains(msg, "!["+"Slide 1") {
		// belt: no image line carries the presigned URL
		for _, line := range strings.Split(msg, "\n") {
			if strings.HasPrefix(line, "![") && strings.Contains(line, "X-Amz") {
				t.Errorf("an image is presigned: %s", line)
			}
		}
	}
	if !strings.Contains(msg, "[Download all slides (carousel.zip)](http://store.invalid/documents/co/th/doc-1.zip?X-Amz-Signature=abc)") {
		t.Errorf("the zip link is missing or not presigned:\n%s", msg)
	}
	if !strings.Contains(msg, "```text\nAgustus tumbuh 9,8%\n\n#promo #agustus\n```") {
		t.Errorf("the caption is not a copyable fence:\n%s", msg)
	}
	// Alt text is the slide's own words, and a bracket in it cannot end the
	// image early.
	if !strings.Contains(msg, "![Slide 2 — Sorotan. Total Pendapatan: Rp 412 Juta]") {
		t.Errorf("slide 2 alt missing:\n%s", msg)
	}
	if strings.Contains(msg, "[Agustus]") {
		t.Errorf("a bracket survived into an alt:\n%s", msg)
	}
	if !strings.HasPrefix(msg, "Your carousel is ready — 3 slides.") {
		t.Errorf("opening line:\n%s", msg)
	}
}

func TestCarouselMessageWithoutACaptionOrAlts(t *testing.T) {
	res := &docgen.Result{
		Document:    &domain.Document{ID: "doc-2", Filename: "c.zip", Format: domain.DocumentFormatCarousel, PageCount: 2},
		DownloadURL: "http://store.invalid/c.zip",
		Carousel:    &docgen.CarouselManifest{Pages: 2},
	}
	msg := carouselMessage(res)
	if strings.Contains(msg, "```") {
		t.Errorf("an empty caption produced a fence:\n%s", msg)
	}
	if !strings.Contains(msg, "![Slide 1](/api/documents/doc-2/pages/1)") || !strings.Contains(msg, "![Slide 2](/api/documents/doc-2/pages/2)") {
		t.Errorf("slides without alts are not labelled by number:\n%s", msg)
	}
}
