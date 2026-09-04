// Package video is the client for `apps/render`, the Node service that draws a
// videoplan.Plan with Remotion and hands back an MP4 (T-V3).
//
// It is the first outbound dependency `docgen` has ever had, and the shape of
// this package is mostly about that. A PDF fails in-process, in milliseconds,
// with an error the caller can read. A video fails in another container, after
// minutes, in one of three ways that mean completely different things to
// whoever is reading the message:
//
//   - the render service is not configured on this deployment;
//   - it is configured and unreachable, or it broke;
//   - it is fine and the plan was refused.
//
// Collapsing those into "render failed" is what `T-A5` exists because of, so
// they are three exported sentinels and the caller maps them onto its own
// vocabulary.
//
// **No plan ever appears in a log line here.** A plan is the tenant's figures;
// the service itself is careful about this, and a client that logged its
// request body would undo that from our side.
package video

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/report/videoplan"
)

// The three failures, told apart.
var (
	// ErrNotConfigured means no render service is set on this deployment. It is
	// a plain refusal rather than a 500: a deployment without one is a valid
	// deployment, exactly as one without MinIO is, and the format simply does
	// not exist there.
	ErrNotConfigured = errors.New("video rendering is not configured on this deployment")
	// ErrUnavailable is the service being unreachable, slow or broken. This is
	// the only one of the three a retry could fix.
	ErrUnavailable = errors.New("the video render service could not be reached")
	// ErrPlanRejected is the service refusing the plan. Deterministic: the same
	// plan will be refused again, so nothing retries it.
	ErrPlanRejected = errors.New("the video render service refused this plan")
)

// Result is one finished render.
type Result struct {
	Data []byte
	// Frames and Seconds are what the service reports. Seconds is wall clock on
	// the render pod — the number the metering records, and the only measure of
	// what a video actually cost that this process can observe.
	Frames  int
	Seconds float64
}

// StillsResult is one finished carousel: the pages, in order, as JPEG bytes
// (T-G6). Seconds is the same wall clock the video reports.
type StillsResult struct {
	Pages   [][]byte
	Seconds float64
}

// Options configures a Client.
type Options struct {
	// BaseURL of the render service, e.g. http://argentum-render:8090. Empty
	// means the format is unavailable.
	BaseURL string
	// Secret is sent as `x-render-secret`. Empty matches a service with no
	// secret set, which is the developer-machine configuration.
	Secret string
	// Timeout bounds one whole render — submit, poll and fetch. It should sit
	// slightly above the service's own wall clock, so a service that gives up
	// answers us rather than being cut off mid-answer and reported as
	// unreachable.
	Timeout time.Duration
	// PollEvery is how often the job is polled. It is also the fastest a
	// progress callback can fire, which is why it is a second rather than
	// something snappier: the ticket caps `render_progress` at one per second
	// and the cheapest way to honour a cap is not to generate the events.
	PollEvery time.Duration
}

// Client talks to one render service.
type Client struct {
	base   string
	secret string
	// http is used for submit and poll. It carries no timeout of its own: the
	// per-render deadline lives on the context, so one slow poll cannot fail a
	// render that is otherwise fine.
	http      *http.Client
	timeout   time.Duration
	pollEvery time.Duration
}

// New builds a client. A nil return is the honest representation of "this
// deployment has no render service" — every method on a nil *Client answers
// ErrNotConfigured, so callers hold one field instead of a field and a bool.
func New(opts Options) *Client {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		return nil
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Minute
	}
	if opts.PollEvery <= 0 {
		opts.PollEvery = time.Second
	}
	return &Client{
		base:      base,
		secret:    strings.TrimSpace(opts.Secret),
		http:      &http.Client{},
		timeout:   opts.Timeout,
		pollEvery: opts.PollEvery,
	}
}

// Configured reports whether videos can be rendered at all.
func (c *Client) Configured() bool { return c != nil }

// Render submits a plan, waits for it, and returns the MP4.
//
// onProgress is called with 0..1 as the service reports it, at most once per
// poll. It is never called with 1.0 from here — completion is the caller's to
// announce, once, after the bytes are in hand, because a progress event
// reading 1.0 while the upload is still running is a spinner that finishes
// before the download link exists.
func (c *Client) Render(ctx context.Context, plan *videoplan.Plan, onProgress func(float64)) (*Result, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	if plan == nil {
		return nil, fmt.Errorf("%w: no plan", ErrPlanRejected)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	jobID, err := c.submit(ctx, plan, "video")
	if err != nil {
		return nil, err
	}
	// The job is dropped on every exit, including a failed one. The service
	// sweeps on a TTL anyway, but a render that produced tens of megabytes and
	// was then abandoned by its caller should not wait for a sweeper on a pod
	// that has one job at a time.
	defer func() {
		dropCtx, dropCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer dropCancel()
		_ = c.drop(dropCtx, jobID)
	}()

	ticker := time.NewTicker(c.pollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// A deadline here is the render outliving the client's patience.
			// It is `unavailable` rather than `rejected` because the plan may
			// have been perfectly renderable and simply long.
			return nil, fmt.Errorf("%w: no result within %s", ErrUnavailable, c.timeout)
		case <-ticker.C:
		}

		st, err := c.status(ctx, jobID)
		if err != nil {
			return nil, err
		}
		switch st.State {
		case "done":
			data, err := c.fetch(ctx, jobID)
			if err != nil {
				return nil, err
			}
			return &Result{Data: data, Frames: st.Frames, Seconds: st.RenderSeconds}, nil
		case "failed", "cancelled":
			// The service already decided whether this was the caller's fault:
			// it answers 400 on a plan it refused and 500 on its own failure.
			// Re-deciding that here from the message text would be a second
			// classifier able to disagree with the first.
			if st.ClientError {
				return nil, fmt.Errorf("%w: %s", ErrPlanRejected, st.Error)
			}
			return nil, fmt.Errorf("%w: %s", ErrUnavailable, st.Error)
		default:
			if onProgress != nil && st.Progress > 0 && st.Progress < 1 {
				onProgress(st.Progress)
			}
		}
	}
}

// RenderStills submits a still plan and collects its pages one at a time
// (T-G6).
//
// It is Render with the result fetched N times: the same submit, the same
// poll, the same drop on every exit, and `GET /v1/jobs/:id/result/:page` for
// each of the pages the status reports. The service builds no zip (decision
// 5); the caller does, from these bytes, with Go's archive/zip.
func (c *Client) RenderStills(ctx context.Context, plan *videoplan.Plan, onProgress func(float64)) (*StillsResult, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	if plan == nil {
		return nil, fmt.Errorf("%w: no plan", ErrPlanRejected)
	}
	if !plan.Still {
		// The service would refuse this with a 400 anyway; refusing here keeps
		// the round trip and says why in the caller's own vocabulary.
		return nil, fmt.Errorf("%w: a carousel needs a still plan", ErrPlanRejected)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	jobID, err := c.submit(ctx, plan, "stills")
	if err != nil {
		return nil, err
	}
	defer func() {
		dropCtx, dropCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer dropCancel()
		_ = c.drop(dropCtx, jobID)
	}()

	ticker := time.NewTicker(c.pollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: no result within %s", ErrUnavailable, c.timeout)
		case <-ticker.C:
		}

		st, err := c.status(ctx, jobID)
		if err != nil {
			return nil, err
		}
		switch st.State {
		case "done":
			if st.Pages <= 0 {
				return nil, fmt.Errorf("%w: the render service reported a finished stills job with no pages", ErrUnavailable)
			}
			pages := make([][]byte, 0, st.Pages)
			for page := 1; page <= st.Pages; page++ {
				data, err := c.fetchPage(ctx, jobID, page)
				if err != nil {
					return nil, err
				}
				pages = append(pages, data)
			}
			return &StillsResult{Pages: pages, Seconds: st.RenderSeconds}, nil
		case "failed", "cancelled":
			if st.ClientError {
				return nil, fmt.Errorf("%w: %s", ErrPlanRejected, st.Error)
			}
			return nil, fmt.Errorf("%w: %s", ErrUnavailable, st.Error)
		default:
			if onProgress != nil && st.Progress > 0 && st.Progress < 1 {
				onProgress(st.Progress)
			}
		}
	}
}

type startResponse struct {
	JobID        string   `json:"job_id"`
	UnknownKinds []string `json:"unknown_kinds"`
	Error        string   `json:"error"`
}

type statusResponse struct {
	State         string  `json:"state"`
	Progress      float64 `json:"progress"`
	Error         string  `json:"error"`
	Frames        int     `json:"frames"`
	RenderSeconds float64 `json:"render_seconds"`
	// Pages is the page count of a done stills job; absent on a video job.
	Pages int `json:"pages"`
	// ClientError is not on the wire. The status route reports a failure
	// without saying whose it was — only `/result` does, by its status code —
	// so a failed job is resolved with one extra call rather than by parsing
	// the message.
	ClientError bool `json:"-"`
}

// submit starts a job. output is "video" or "stills" — the service refuses a
// plan built for the other one, so the choice is made here, once, by the
// method that knows which result it is going to collect.
func (c *Client) submit(ctx context.Context, plan *videoplan.Plan, output string) (string, error) {
	body, err := json.Marshal(map[string]any{"plan": plan, "output": output})
	if err != nil {
		return "", fmt.Errorf("%w: marshal plan: %v", ErrPlanRejected, err)
	}
	req, err := c.request(ctx, http.MethodPost, "/v1/render", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = res.Body.Close() }()

	var out startResponse
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out)
	switch {
	case res.StatusCode == http.StatusAccepted && out.JobID != "":
		return out.JobID, nil
	case res.StatusCode == http.StatusBadRequest:
		return "", fmt.Errorf("%w: %s", ErrPlanRejected, message(out.Error))
	default:
		return "", fmt.Errorf("%w: render service answered %d: %s",
			ErrUnavailable, res.StatusCode, message(out.Error))
	}
}

func (c *Client) status(ctx context.Context, jobID string) (*statusResponse, error) {
	req, err := c.request(ctx, http.MethodGet, "/v1/jobs/"+jobID, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: job status answered %d", ErrUnavailable, res.StatusCode)
	}
	var out statusResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: unreadable job status: %v", ErrUnavailable, err)
	}
	if out.State == "failed" || out.State == "cancelled" {
		out.ClientError = c.failureIsTheCallers(ctx, jobID)
	}
	return &out, nil
}

// failureIsTheCallers asks `/result` whose fault a failed job was. A 400 is the
// service saying the plan was bad; anything else is its own failure, and an
// unanswered question defaults to ours — telling a tenant their spec is wrong
// when we do not know that is worse than owning an outage we did not have.
func (c *Client) failureIsTheCallers(ctx context.Context, jobID string) bool {
	req, err := c.request(ctx, http.MethodGet, "/v1/jobs/"+jobID+"/result", nil)
	if err != nil {
		return false
	}
	res, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	return res.StatusCode == http.StatusBadRequest
}

func (c *Client) fetch(ctx context.Context, jobID string) ([]byte, error) {
	req, err := c.request(ctx, http.MethodGet, "/v1/jobs/"+jobID+"/result", nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusBadRequest {
		return nil, fmt.Errorf("%w: %s", ErrPlanRejected, readError(res.Body))
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: result answered %d", ErrUnavailable, res.StatusCode)
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: reading the video: %v", ErrUnavailable, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: the render service returned an empty file", ErrUnavailable)
	}
	return data, nil
}

// fetchPage reads one page of a done stills job.
func (c *Client) fetchPage(ctx context.Context, jobID string, page int) ([]byte, error) {
	req, err := c.request(ctx, http.MethodGet, fmt.Sprintf("/v1/jobs/%s/result/%d", jobID, page), nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: page %d answered %d", ErrUnavailable, page, res.StatusCode)
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: reading page %d: %v", ErrUnavailable, page, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: page %d is empty", ErrUnavailable, page)
	}
	return data, nil
}

func (c *Client) drop(ctx context.Context, jobID string) error {
	req, err := c.request(ctx, http.MethodDelete, "/v1/jobs/"+jobID, nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
	return nil
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if c.secret != "" {
		req.Header.Set("x-render-secret", c.secret)
	}
	return req, nil
}

func readError(r io.Reader) string {
	var out struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&out)
	return message(out.Error)
}

// message keeps a service-supplied string usable in a tenant-facing error
// without letting it become the whole error. Long is truncated rather than
// dropped: the first sentence of a Remotion failure is usually the useful one.
func message(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "no reason given"
	}
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}
