package video

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/report/videoplan"
)

func stillPlan() *videoplan.Plan {
	return &videoplan.Plan{Version: 1, Width: 1080, Height: 1350, FPS: 1, TotalFrames: 3, Still: true,
		Scenes: []videoplan.Scene{{Kind: "cover", Frames: 1}, {Kind: "kpi", Frames: 1}, {Kind: "closing", Frames: 1}}}
}

// stillsServer is the render service's stills contract (T-G5), as the client
// sees it: accept with output=stills, report pages when done, serve each page
// on /result/:page, and answer the bare /result with a 409.
func stillsServer(t *testing.T, pages int) (http.Handler, *int32, *int32) {
	t.Helper()
	var polls, dropped int32
	const id = "22222222-2222-2222-2222-222222222222"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/render":
			body := make([]byte, 1<<16)
			n, _ := r.Body.Read(body)
			if !strings.Contains(string(body[:n]), `"output":"stills"`) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"expected output=stills"}`))
				return
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprintf(w, `{"job_id":%q}`, id)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/"+id:
			if atomic.AddInt32(&polls, 1) < 2 {
				_, _ = w.Write([]byte(`{"state":"rendering","progress":0.5,"output":"stills"}`))
				return
			}
			fmt.Fprintf(w, `{"state":"done","progress":1,"output":"stills","frames":%d,"pages":%d,"render_seconds":5.3}`, pages, pages)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/"+id+"/result":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"a stills job has pages; fetch /result/:page"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/jobs/"+id+"/result/"):
			var page int
			fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/v1/jobs/"+id+"/result/"), "%d", &page)
			if page < 1 || page > pages {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			fmt.Fprintf(w, "JPEG-PAGE-%d", page)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/jobs/"+id:
			atomic.AddInt32(&dropped, 1)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}), &polls, &dropped
}

// TestRenderStillsCollectsEveryPage walks the happy path: submitted as stills,
// polled to done, every page fetched in order, the job dropped afterwards.
func TestRenderStillsCollectsEveryPage(t *testing.T) {
	h, _, dropped := stillsServer(t, 3)
	c := newClient(t, h)

	var seen []float64
	res, err := c.RenderStills(context.Background(), stillPlan(), func(f float64) { seen = append(seen, f) })
	if err != nil {
		t.Fatalf("RenderStills: %v", err)
	}
	if len(res.Pages) != 3 {
		t.Fatalf("got %d pages, want 3", len(res.Pages))
	}
	for i, p := range res.Pages {
		if want := fmt.Sprintf("JPEG-PAGE-%d", i+1); string(p) != want {
			t.Errorf("page %d = %q, want %q — pages out of order", i+1, p, want)
		}
	}
	if res.Seconds != 5.3 {
		t.Errorf("seconds = %v, want 5.3", res.Seconds)
	}
	if len(seen) == 0 {
		t.Error("no progress reported")
	}
	if atomic.LoadInt32(dropped) != 1 {
		t.Errorf("job dropped %d times, want 1", *dropped)
	}
}

// A video plan is refused before the round trip: the service would answer 400,
// and the client says so in its own vocabulary without a browser starting.
func TestRenderStillsRefusesAVideoPlan(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the service was called for a plan the client should refuse")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	_, err := c.RenderStills(context.Background(), plan(), nil)
	if !errors.Is(err, ErrPlanRejected) {
		t.Fatalf("err = %v, want ErrPlanRejected", err)
	}
}

// A done job reporting no pages is the service's failure, not the plan's: the
// caller retries rather than being told its spec is wrong.
func TestADoneStillsJobWithNoPagesIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job_id":"33333333-3333-3333-3333-333333333333"}`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"state":"done","progress":1,"pages":0}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := New(Options{BaseURL: srv.URL, Timeout: time.Second, PollEvery: time.Millisecond})
	_, err := c.RenderStills(context.Background(), stillPlan(), nil)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// A nil client answers the same sentinel for stills as for video.
func TestRenderStillsOnANilClientIsNotConfigured(t *testing.T) {
	var c *Client
	if _, err := c.RenderStills(context.Background(), stillPlan(), nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}
