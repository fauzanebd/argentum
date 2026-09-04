package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

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
	if err == nil || !strings.Contains(err.Error(), "a carousel is 2–10 slides") {
		t.Fatalf("Execute = %v, want the slide-band refusal", err)
	}
	if len(q.jobs) != 0 {
		t.Error("a refused carousel was queued anyway")
	}
}
