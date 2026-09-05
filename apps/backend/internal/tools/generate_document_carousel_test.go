package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// renderingTool is videoTool by another name: a deployment with a render
// service and a queue, the only configuration in which mp4 and carousel are
// offered.
func renderingTool(t *testing.T) (*GenerateDocumentTool, *recordingQueue) {
	t.Helper()
	return videoTool(t)
}

func carouselArgs(sections int) string {
	secs := []string{
		`{"type":"cover","text":"Penjualan Agustus","period":"AGUSTUS 2026"}`,
		`{"type":"kpi_row","items":[{"label":"Total Pendapatan","value":412350000,"fmt":"currency"}]}`,
		`{"type":"paragraph","text":"Pendapatan naik 9,8% dibanding Juli, didorong kanal WhatsApp yang tumbuh paling cepat."}`,
	}
	for i := len(secs); i < sections; i++ {
		secs = append(secs, fmt.Sprintf(`{"type":"heading","level":1,"text":"Bagian %d"}`, i))
	}
	return `{"spec_version":2,"format":"carousel","title":"Penjualan Agustus","locale":"id","currency":"IDR",` +
		`"social":{"caption":"Agustus tumbuh 9,8%","hashtags":["promo"]},` +
		`"content":{"sections":[` + strings.Join(secs, ",") + `]}}`
}

// The enum rides the video's condition: a render service and a queue, or
// neither format is offered — a format the description promises and the
// process cannot finish is the failure T-V3 wrote WithVideoQueue for.
func TestCarouselIsOfferedOnlyWhereItCanBeFinished(t *testing.T) {
	with, _ := renderingTool(t)
	if !hasFormat(with, "carousel") || !hasFormat(with, "mp4") {
		t.Errorf("a rendering tool offers %v", with.formats())
	}
	if !strings.Contains(with.Description(), `"carousel"`) {
		t.Error("the description does not explain the carousel format")
	}

	without, _ := newTool()
	if hasFormat(without, "carousel") {
		t.Errorf("a tool with no queue offers %v", without.formats())
	}
	ctx := tenantctx.WithThreadID(tenantctx.WithCompanyID(context.Background(), "co-1"), "th-1")
	_, err := without.Execute(ctx, carouselArgs(3))
	if err == nil || !strings.Contains(err.Error(), "carousel rendering is not available") {
		t.Errorf("Execute without a queue = %v", err)
	}
}

// A carousel leaves the turn the way a video does: queued, and the model told
// what to say — in the carousel's own words.
func TestACarouselIsQueuedAndTheModelIsToldSo(t *testing.T) {
	tool, q := renderingTool(t)
	ctx := tenantctx.WithThreadID(tenantctx.WithCompanyID(context.Background(), "co-1"), "th-1")

	out, err := tool.Execute(ctx, carouselArgs(3))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if body["status"] != "rendering" || body["format"] != "carousel" {
		t.Errorf("answer = %v", body)
	}
	note, _ := body["note"].(string)
	if !strings.Contains(note, "carousel") || strings.Contains(note, "video") {
		t.Errorf("the note talks about the wrong artifact: %q", note)
	}
	if len(q.jobs) != 1 || q.jobs[0].Spec.Format != "carousel" || q.jobs[0].Spec.Social == nil {
		t.Fatalf("queued %+v", q.jobs)
	}
}

// The slide band is enforced in the turn, where the model can still shorten
// the spec: eleven beats never reach the queue.
func TestTooManySlidesAreRefusedInTheTurn(t *testing.T) {
	tool, q := renderingTool(t)
	ctx := tenantctx.WithThreadID(tenantctx.WithCompanyID(context.Background(), "co-1"), "th-1")

	_, err := tool.Execute(ctx, carouselArgs(12))
	if err == nil || !strings.Contains(err.Error(), "a carousel is 1–10 slides") {
		t.Fatalf("Execute = %v, want the slide-band refusal", err)
	}
	if len(q.jobs) != 0 {
		t.Error("a refused carousel was queued anyway")
	}
}

// --- T-G12: the promotion card's photograph, resolved at the door ---

// fakeImages is a tenant's picture library, resolving by id or name and
// company-scoped like the real one.
type fakeImages struct {
	rows    []*domain.PostImage
	bodies  map[string][]byte
	missing bool // the row exists and its object does not
}

func (f *fakeImages) Resolve(_ context.Context, companyID, ref string) *domain.PostImage {
	for _, r := range f.rows {
		if r.CompanyID != companyID {
			continue
		}
		if r.ID == ref || strings.EqualFold(r.Name, strings.TrimSpace(ref)) {
			return r
		}
	}
	return nil
}

func (f *fakeImages) Bytes(_ context.Context, companyID, id string) (*domain.PostImage, []byte, error) {
	if f.missing {
		return nil, nil, errors.New("no such object")
	}
	for _, r := range f.rows {
		if r.ID == id && r.CompanyID == companyID {
			return r, f.bodies[id], nil
		}
	}
	return nil, nil, domain.ErrNotFound
}

const promoArgs = `{"format":"carousel","title":"Promo","social":{"caption":"Diskon jeruk"},
 "content":{"sections":[{"type":"promo","badge":"CRAZY DEAL","title":"Jeruk Sunkist Cara Cara",
 "image":"jeruk cara cara","was":{"v":5980,"fmt":"currency"},"price":{"v":3370,"fmt":"currency"},
 "unit":"/100 gram"}]}}`

func promoTool(t *testing.T, lib *fakeImages) (*GenerateDocumentTool, *recordingQueue) {
	t.Helper()
	tool, q := videoTool(t)
	if lib != nil {
		tool = tool.WithImages(lib)
	}
	return tool, q
}

func promoLibrary() *fakeImages {
	return &fakeImages{
		rows:   []*domain.PostImage{{ID: "img-1", CompanyID: "co-1", Name: "Jeruk Cara Cara", Alt: "Jeruk dibelah", Width: 900, Height: 600}},
		bodies: map[string][]byte{"img-1": []byte("PNGBYTES")},
	}
}

// The picture is resolved in the turn, and the queued job carries the bytes
// and the resolved id — so the worker draws exactly what the turn reported.
func TestThePromoImageIsResolvedAtTheDoor(t *testing.T) {
	tool, q := promoTool(t, promoLibrary())

	out, err := tool.Execute(videoTurnCtx(), promoArgs)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res["status"] != "rendering" {
		t.Errorf("status = %v", res["status"])
	}
	used, _ := res["images_used"].([]any)
	if len(used) != 1 || used[0] != "Jeruk Cara Cara" {
		t.Errorf("images_used = %v, want the library's own name", res["images_used"])
	}
	if _, bad := res["images_not_found"]; bad {
		t.Errorf("a resolved image was reported missing: %v", res["images_not_found"])
	}

	job := q.jobs[0]
	if len(job.Images) != 1 {
		t.Fatalf("the job carries %d images", len(job.Images))
	}
	img, ok := job.Images["img-1"]
	if !ok || string(img.PNG) != "PNGBYTES" {
		t.Errorf("the job carries no bytes for img-1: %+v", job.Images)
	}
	if img.Aspect != 1.5 || img.Alt != "Jeruk dibelah" {
		t.Errorf("aspect=%v alt=%q", img.Aspect, img.Alt)
	}
	// The resolved id is written back onto the spec, so the worker never
	// re-resolves a name and cannot pick a different picture.
	if got := job.Spec.Content.Sections[0].ImageID; got != "img-1" {
		t.Errorf("the queued spec carries image_id %q", got)
	}
}

// A name nobody uploaded is not a failed turn. The card is drawn without a
// photograph and the model is told which name missed and what to say.
func TestAnUnknownImageNameIsReportedNotRefused(t *testing.T) {
	tool, q := promoTool(t, &fakeImages{})

	out, err := tool.Execute(videoTurnCtx(), promoArgs)
	if err != nil {
		t.Fatalf("an unknown image name refused the turn: %v", err)
	}
	var res map[string]any
	_ = json.Unmarshal([]byte(out), &res)
	missing, _ := res["images_not_found"].([]any)
	if len(missing) != 1 || missing[0] != "jeruk cara cara" {
		t.Errorf("images_not_found = %v", res["images_not_found"])
	}
	note, _ := res["images_note"].(string)
	for _, want := range []string{"without a photograph", "upload"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note omits %q: %s", want, note)
		}
	}
	if len(q.jobs) != 1 || len(q.jobs[0].Images) != 0 {
		t.Errorf("the job should still be queued, with no images: %+v", q.jobs)
	}
}

// A row whose object has gone is a miss too, for the same reason: a promotion
// is not worth failing a turn over.
func TestAnImageWithNoObjectIsAMiss(t *testing.T) {
	lib := promoLibrary()
	lib.missing = true
	tool, q := promoTool(t, lib)

	out, err := tool.Execute(videoTurnCtx(), promoArgs)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var res map[string]any
	_ = json.Unmarshal([]byte(out), &res)
	if _, bad := res["images_not_found"]; !bad {
		t.Error("a row with no object was not reported missing")
	}
	if len(q.jobs[0].Images) != 0 {
		t.Error("the job carries an image whose bytes could not be read")
	}
}

// Another company's picture is invisible, even by exact name.
func TestThePictureLibraryIsCompanyScopedAtTheDoor(t *testing.T) {
	lib := promoLibrary()
	lib.rows[0].CompanyID = "co-2"
	tool, _ := promoTool(t, lib)

	out, err := tool.Execute(videoTurnCtx(), promoArgs) // the turn is co-1's
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var res map[string]any
	_ = json.Unmarshal([]byte(out), &res)
	if _, bad := res["images_not_found"]; !bad {
		t.Error("another company's picture resolved")
	}
}

// A deployment with no library still draws promotion cards, without pictures.
func TestNoLibraryStillQueuesThePromo(t *testing.T) {
	tool, q := promoTool(t, nil)

	out, err := tool.Execute(videoTurnCtx(), promoArgs)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var res map[string]any
	_ = json.Unmarshal([]byte(out), &res)
	if _, bad := res["images_not_found"]; !bad {
		t.Error("a deployment with no library did not say the picture was missing")
	}
	if len(q.jobs) != 1 {
		t.Fatalf("%d jobs queued", len(q.jobs))
	}
}
