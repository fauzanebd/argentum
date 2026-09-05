package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/report/video"
	"github.com/fauzanebd/argentum/internal/report/videoplan"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// generate_document and `mp4` (T-V3).
//
// The whole of this file is about one decision: a video leaves the turn. A
// tool call that waited four minutes for one would spend T-16's entire
// iteration budget on waiting, and then have nothing left to write the reply
// with — the shape `5ca4ca6` and `45c1142` were both fixes for.

type recordingQueue struct {
	jobs []queue.ReportRenderPayload
}

func (q *recordingQueue) EnqueueReportRender(_ context.Context, p queue.ReportRenderPayload) (string, error) {
	q.jobs = append(q.jobs, p)
	return "task-1", nil
}

// videoArgs is an analytical report, which is what the format requires.
const videoArgs = `{"format":"mp4","title":"June review","content":{"sections":[
  {"type":"kpi_row","items":[{"label":"Revenue","value":{"v":4012118800,"fmt":"currency"}}]},
  {"type":"paragraph","text":"Revenue closed June 3.9% above May, the third consecutive month of growth, and all of it came from the North region where two enterprise accounts renewed early. Refunds rose in the same period and are the figure to watch."}
]}}`

func videoTool(t *testing.T) (*GenerateDocumentTool, *recordingQueue) {
	t.Helper()
	q := &recordingQueue{}
	svc := docgen.New(&memStore{objects: map[string][]byte{}}, &memDocs{}, nil, nil, nil, time.Hour).
		WithVideo(video.New(video.Options{BaseURL: "http://127.0.0.1:1"}), videoplan.Limits{})
	return NewGenerateDocumentTool(svc).WithVideoQueue(q), q
}

func videoTurnCtx() context.Context {
	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")
	return tenantctx.WithThreadID(ctx, "th-1")
}

// The hand-off: the tool returns immediately, the job is queued, and the model
// is told what to say. A render client pointed at a dead port proves the first
// part — a tool that rendered inline would hang here rather than pass.
func TestVideoIsHandedToTheQueue(t *testing.T) {
	tool, q := videoTool(t)

	out, err := tool.Execute(videoTurnCtx(), videoArgs)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("result: %v", err)
	}
	if res["status"] != "rendering" {
		t.Errorf("status = %v, want rendering", res["status"])
	}
	if res["download_url"] != nil {
		t.Error("the tool offered a link to a file that does not exist yet")
	}
	// The note is what the model reads, and the two failure modes it prevents
	// are the model claiming the file is ready and the model calling the tool
	// again because nothing came back.
	note, _ := res["note"].(string)
	for _, want := range []string{"posted into this conversation", "do not say it is ready", "do not call this tool again"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note omits %q: %s", want, note)
		}
	}
	if len(q.jobs) != 1 {
		t.Fatalf("%d jobs queued, want 1", len(q.jobs))
	}
	if q.jobs[0].ThreadID != "th-1" || q.jobs[0].CompanyID != "co-1" {
		t.Errorf("the job cannot find its way back: %+v", q.jobs[0])
	}
	if q.jobs[0].ReportID != "" {
		t.Error("an agent's video carries a report id; nothing would collect it")
	}
}

// The agent id travels, because the render happens after the turn that asked
// for it has ended — nothing else in the payload could attribute the spend.
func TestTheQueuedVideoCarriesItsAgent(t *testing.T) {
	tool, q := videoTool(t)
	ctx := agentscope.WithScope(videoTurnCtx(), agentscope.Scope{AgentID: "agent-7"})

	if _, err := tool.Execute(ctx, videoArgs); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if q.jobs[0].AgentID != "agent-7" {
		t.Errorf("agent id = %q, want agent-7", q.jobs[0].AgentID)
	}
}

// Everything that can refuse the spec runs inside the turn, where the model can
// still repair it. A refusal from the worker four minutes later reaches nobody:
// the user has already been told a video is coming.
func TestAnUnrenderableVideoIsRefusedInTheTurn(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{
			name: "a record rather than an argument",
			args: `{"format":"mp4","title":"Invoice","content":{"sections":[
			  {"type":"key_value","items":[{"label":"Invoice","value":"INV-1042"}]}]}}`,
			want: "pdf",
		},
		{
			name: "figures with no reading of them",
			args: `{"format":"mp4","title":"June","content":{"sections":[
			  {"type":"kpi_row","items":[{"label":"Revenue","value":{"v":1,"fmt":"currency"}}]}]}}`,
			want: "interpret",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, q := videoTool(t)
			_, err := tool.Execute(videoTurnCtx(), tc.args)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not mention %q: %v", tc.want, err)
			}
			if len(q.jobs) != 0 {
				t.Fatal("a refused spec was queued anyway")
			}
		})
	}
}

// A deployment that cannot finish a video does not offer one.
//
// This is the `list_watchers` lesson of 2026-08-04 one door further out: there,
// an MCP client was advertised a tool that did not exist; here, the model would
// promise a customer a file. Both halves have to be present — a render service
// to draw it and a queue to hand it to — so both are checked.
func TestTheFormatEnumFollowsWhatThisProcessCanFinish(t *testing.T) {
	full, _ := videoTool(t)
	if !hasFormat(full, "mp4") {
		t.Error("a process with a render service and a queue does not offer mp4")
	}
	if !strings.Contains(full.Description(), "mp4") {
		t.Error("the description does not mention mp4 where it is available")
	}

	noQueue := NewGenerateDocumentTool(
		docgen.New(&memStore{objects: map[string][]byte{}}, &memDocs{}, nil, nil, nil, time.Hour).
			WithVideo(video.New(video.Options{BaseURL: "http://127.0.0.1:1"}), videoplan.Limits{}))
	if hasFormat(noQueue, "mp4") {
		t.Error("a process with no queue offers mp4; nothing would ever finish it")
	}

	noService, _ := newTool()
	withQueue := noService.WithVideoQueue(&recordingQueue{})
	if hasFormat(withQueue, "mp4") {
		t.Error("a deployment with no render service offers mp4")
	}
	if strings.Contains(withQueue.Description(), "mp4") {
		t.Error("the description mentions mp4 where it cannot be produced")
	}
}

// And asking for it anyway is refused rather than queued.
func TestMP4IsRefusedWhereItIsNotAvailable(t *testing.T) {
	tool, _ := newTool()
	_, err := tool.Execute(videoTurnCtx(), videoArgs)
	if err == nil {
		t.Fatal("a video was accepted on a deployment that cannot render one")
	}
	if !strings.Contains(err.Error(), "pdf") {
		t.Errorf("the refusal does not name a format that works: %v", err)
	}
}

func hasFormat(t *GenerateDocumentTool, want string) bool {
	for _, v := range t.Parameters()["format"].Enum {
		if s, ok := v.(string); ok && s == want {
			return true
		}
	}
	return false
}

// The target travels (T-G6, finding 6). A render answers minutes after the
// turn that asked, and the turn's phone number or channel id is on the turn's
// payload and nowhere a later job can read it — so the tool copies it from
// the context the runner set, and a context without one queues a job whose
// only destination is the thread.
func TestTheQueuedRenderCarriesTheReplyTarget(t *testing.T) {
	tool, q := videoTool(t)
	target := tenantctx.ReplyTarget{Channel: "whatsapp", PhoneNumber: "+628123"}

	if _, err := tool.Execute(tenantctx.WithReplyTarget(videoTurnCtx(), target), videoArgs); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if q.jobs[0].Target == nil || *q.jobs[0].Target != target {
		t.Errorf("target = %+v, want %+v", q.jobs[0].Target, target)
	}

	if _, err := tool.Execute(videoTurnCtx(), videoArgs); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if q.jobs[1].Target != nil {
		t.Errorf("a turn with no reply target queued one: %+v", q.jobs[1].Target)
	}
}
