package video

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/report/videoplan"
)

// plan is the smallest thing the client will send. Nothing here validates it —
// the render service does, which is the point of the rejection tests below.
func plan() *videoplan.Plan {
	return &videoplan.Plan{Version: 1, Width: 1920, Height: 1080, FPS: 30, TotalFrames: 30}
}

func newClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(Options{BaseURL: srv.URL, Timeout: 5 * time.Second, PollEvery: time.Millisecond})
}

// TestNoBaseURLIsNotConfigured pins the shape the whole package leans on: a nil
// client is the honest representation of a deployment with no render service,
// and every path through it answers the same sentinel rather than panicking.
func TestNoBaseURLIsNotConfigured(t *testing.T) {
	c := New(Options{})
	if c != nil {
		t.Fatal("an empty base URL should produce a nil client")
	}
	if c.Configured() {
		t.Fatal("a nil client reports itself configured")
	}
	if _, err := c.Render(context.Background(), plan(), nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Render on a nil client = %v, want ErrNotConfigured", err)
	}
}

// TestRenderReturnsTheVideo walks the happy path and asserts the two things the
// caller downstream depends on: the bytes, and the render seconds that become
// a usage row.
func TestRenderReturnsTheVideo(t *testing.T) {
	var polls int32
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/render":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job_id":"11111111-1111-1111-1111-111111111111"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/11111111-1111-1111-1111-111111111111":
			if atomic.AddInt32(&polls, 1) < 3 {
				_, _ = w.Write([]byte(`{"state":"rendering","progress":0.4}`))
				return
			}
			_, _ = w.Write([]byte(`{"state":"done","progress":1,"frames":900,"render_seconds":42.5}`))
		case r.URL.Path == "/v1/jobs/11111111-1111-1111-1111-111111111111/result":
			_, _ = w.Write([]byte("MP4BYTES"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	var seen []float64
	res, err := c.Render(context.Background(), plan(), func(f float64) { seen = append(seen, f) })
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(res.Data) != "MP4BYTES" {
		t.Fatalf("data = %q", res.Data)
	}
	if res.Seconds != 42.5 || res.Frames != 900 {
		t.Fatalf("stats = %v seconds, %d frames", res.Seconds, res.Frames)
	}
	if len(seen) == 0 {
		t.Fatal("no progress reported over three polls")
	}
	// Never 1.0 from the client: the caller announces completion once, after
	// the file exists. A bar that fills while the upload runs is a bar that
	// lies, and this is the only place that rule can be enforced cheaply.
	for _, f := range seen {
		if f >= 1 {
			t.Fatalf("progress reported %v; completion is the caller's to announce", f)
		}
	}
}

// TestARefusedPlanIsTheCallers is the distinction the package exists for: an
// integrator whose spec is wrong and an operator whose service is down must not
// read the same sentence.
func TestARefusedPlanIsTheCallers(t *testing.T) {
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"plan version 9 is not supported"}`))
	}))
	_, err := c.Render(context.Background(), plan(), nil)
	if !errors.Is(err, ErrPlanRejected) {
		t.Fatalf("err = %v, want ErrPlanRejected", err)
	}
	if !contains(err.Error(), "version 9") {
		t.Fatalf("the service's own reason was dropped: %v", err)
	}
}

// TestAFailedJobAsksWhoseFaultItWas covers the case the status route cannot
// answer on its own: it reports `failed` without saying whose failure it was,
// so the client asks `/result`, whose status code is the service's own verdict.
func TestAFailedJobAsksWhoseFaultItWas(t *testing.T) {
	for _, tc := range []struct {
		name       string
		resultCode int
		want       error
	}{
		{"the plan was bad", http.StatusBadRequest, ErrPlanRejected},
		{"the renderer broke", http.StatusInternalServerError, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost:
					w.WriteHeader(http.StatusAccepted)
					_, _ = w.Write([]byte(`{"job_id":"22222222-2222-2222-2222-222222222222"}`))
				case contains(r.URL.Path, "/result"):
					w.WriteHeader(tc.resultCode)
					_, _ = w.Write([]byte(`{"error":"boom"}`))
				default:
					_, _ = w.Write([]byte(`{"state":"failed","error":"boom"}`))
				}
			}))
			if _, err := c.Render(context.Background(), plan(), nil); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestAnUnreachableServiceIsUnavailable is the third failure, and the only one
// a retry could fix.
func TestAnUnreachableServiceIsUnavailable(t *testing.T) {
	c := New(Options{BaseURL: "http://127.0.0.1:1", Timeout: time.Second, PollEvery: time.Millisecond})
	if _, err := c.Render(context.Background(), plan(), nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// TestTheJobIsDroppedAfterCollection asserts the cleanup, because the service
// holds one job at a time on disk and a caller that never collects leaves tens
// of megabytes to a TTL sweeper.
func TestTheJobIsDroppedAfterCollection(t *testing.T) {
	dropped := make(chan struct{}, 1)
	c := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			select {
			case dropped <- struct{}{}:
			default:
			}
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"job_id":"33333333-3333-3333-3333-333333333333"}`))
		case contains(r.URL.Path, "/result"):
			_, _ = w.Write([]byte("BYTES"))
		default:
			_, _ = w.Write([]byte(`{"state":"done","render_seconds":1}`))
		}
	}))
	if _, err := c.Render(context.Background(), plan(), nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	select {
	case <-dropped:
	case <-time.After(2 * time.Second):
		t.Fatal("the job was never dropped")
	}
}

// TestTheSecretTravels pins the one header the service authenticates on.
func TestTheSecretTravels(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case got <- r.Header.Get("x-render-secret"):
		default:
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"no"}`))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL, Secret: "s3cret", Timeout: time.Second, PollEvery: time.Millisecond})
	_, _ = c.Render(context.Background(), plan(), nil)
	if h := <-got; h != "s3cret" {
		t.Fatalf("x-render-secret = %q", h)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
