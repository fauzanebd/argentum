package llmtap

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The four properties this file has to hold, and the first two are the ones a
// capture proxy failed at:
//
//  1. It changes nothing about the request the provider receives.
//  2. It is entirely absent when no directory is configured.
//  3. The file has the body verbatim.
//  4. The file has no credential in it.

// recordingBase stands in for the network and keeps what it was handed.
type recordingBase struct {
	body   []byte
	header http.Header
	calls  int
}

func (r *recordingBase) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls++
	if req.Body != nil {
		r.body, _ = io.ReadAll(req.Body)
	}
	r.header = req.Header.Clone()
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader("{}")),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

func request(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "https://openrouter.ai/api/v1/chat/completions",
		bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer sk-or-v1-averyrealsecret")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func captured(t *testing.T, dir string) string {
	t.Helper()
	names, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read capture dir: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("wrote %d files, want one", len(names))
	}
	raw, err := os.ReadFile(filepath.Join(dir, names[0].Name()))
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	return string(raw)
}

// **The property the capture proxy broke.** Reading a request body consumes it,
// and a request whose body has been consumed reaches the provider empty — which
// is how three proxy variants turned a working turn into "this turn finished
// without producing an answer".
func TestTheRequestReachesTheProviderUnchanged(t *testing.T) {
	base := &recordingBase{}
	body := `{"model":"kimi","messages":[{"role":"system","content":"<<<WORKSPACE_PROCEDURE"}]}`
	rt := New(base, t.TempDir())

	if _, err := rt.RoundTrip(request(t, body)); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if string(base.body) != body {
		t.Errorf("the provider was sent %q, want the original body", string(base.body))
	}
	if base.header.Get("Authorization") == "" {
		t.Error("the tap stripped the credential from the outbound request; only the file is redacted")
	}
}

// A request replayed on a redirect or a retried HTTP/2 stream goes through
// GetBody, which would otherwise still point at the reader the tap drained.
func TestTheBodyCanStillBeReplayed(t *testing.T) {
	body := `{"model":"kimi"}`
	req := request(t, body)
	rt := New(&recordingBase{}, t.TempDir()).(*Transport)

	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	replay, err := req.GetBody()
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	again, _ := io.ReadAll(replay)
	if string(again) != body {
		t.Errorf("the replayed body is %q, want the original", string(again))
	}
}

// Off is the state every deployment is in, and off means the chain does not
// change shape — the base transport is what the client gets.
func TestNoDirectoryLeavesTheChainUntouched(t *testing.T) {
	base := &recordingBase{}
	if got := New(base, "  "); got != http.RoundTripper(base) {
		t.Errorf("New returned %T with no directory, want the base transport itself", got)
	}
}

// What the row asks for is the bytes. A file that had been through a JSON
// encoder would reorder keys and re-escape strings, which is the exact class of
// difference the capture exists to rule out.
func TestTheCaptureHasTheBodyVerbatim(t *testing.T) {
	dir := t.TempDir()
	body := `{"messages":[{"role":"tool","content":"<<<WORKSPACE_PROCEDURE name=\"Weekly report\">>>\nStep 1.\n<<<END_WORKSPACE_PROCEDURE>>>"}]}`
	if _, err := New(&recordingBase{}, dir).RoundTrip(request(t, body)); err != nil {
		t.Fatalf("round trip: %v", err)
	}

	got := captured(t, dir)
	if !strings.Contains(got, body) {
		t.Errorf("the capture does not contain the body verbatim:\n%s", got)
	}
	if !strings.HasPrefix(got, "POST https://openrouter.ai/api/v1/chat/completions\n") {
		t.Errorf("the capture does not open with the request line:\n%s", got)
	}
}

// The working contract's rule is absolute, and a debug file is a place somebody
// eventually attaches to a ticket.
func TestTheCaptureCarriesNoCredential(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(&recordingBase{}, dir).RoundTrip(request(t, `{}`)); err != nil {
		t.Fatalf("round trip: %v", err)
	}

	got := captured(t, dir)
	if strings.Contains(got, "sk-or-v1-averyrealsecret") {
		t.Errorf("the api key was written to disk:\n%s", got)
	}
	if !strings.Contains(got, "Authorization: [redacted]") {
		t.Errorf("the header was dropped rather than redacted, which hides that it was sent:\n%s", got)
	}
	if !strings.Contains(got, "Content-Type: application/json") {
		t.Errorf("an ordinary header was redacted too:\n%s", got)
	}
}

// A debug switch that can fail a turn is a debug switch that changes the
// behaviour it was turned on to observe.
func TestAnUnwritableDirectoryDoesNotFailTheTurn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	base := &recordingBase{}
	rt := New(base, dir)
	// Remove the directory out from under the tap, which is the closest
	// reachable stand-in for a disk that fills up mid-capture.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}

	resp, err := rt.RoundTrip(request(t, `{}`))
	if err != nil {
		t.Fatalf("a failed capture failed the request: %v", err)
	}
	if resp.StatusCode != 200 || base.calls != 1 {
		t.Errorf("the request did not reach the provider: status=%d calls=%d", resp.StatusCode, base.calls)
	}
}
