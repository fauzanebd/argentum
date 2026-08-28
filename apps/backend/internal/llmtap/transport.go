// Package llmtap writes the outbound inference request to disk, exactly as the
// provider receives it.
//
// **It exists because a capture proxy could not answer the question and three
// shapes of one failed trying.** `T-K2` asks for one row of evidence: that a
// skill body reaches the model inside `skill.Frame`, unfenced, on the wire. The
// system-prompt half was captured by standing a proxy in front of OpenRouter;
// the tool-result half was not, because the agent's provider client streams and
// every proxy variant tried either buffered the response or relayed it in a way
// that client would not read. Two turns came back as "this turn finished
// without producing an answer" — which reads exactly like the model declining
// to open a skill, i.e. like the feature's own design failing. It was the
// proxy. `docs/coverage/live-gate-backlog.md` §1p records the afternoon and
// names this as the technique that should have been used instead.
//
// A transport has none of that surface. It sits in the same chain `llmzdr` and
// `llmusage` already occupy, tees the request body before handing it to the
// real transport, and touches the response not at all — so there is no network
// hop to be wrong about the stream, and it works identically for whatever
// provider a deployment is pointed at.
//
// **It is off unless a directory is named, and it must stay that way outside a
// gate.** What it writes is the composed prompt: the tenant's persona, their
// company profile, their procedures, and the user's question. That is tenant
// data sitting in a file, so this is a switch somebody turns on to answer a
// question and turns off again, not an observability feature.
package llmtap

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// redactedHeaders are the headers whose values never reach the file.
//
// **Matched as a prefix over the lowercased name**, so `x-api-key`,
// `authorization` and `x-goog-api-key` are all covered and a provider that
// invents `x-anthropic-api-key` tomorrow is covered by the `x-` entries it
// already matches. The rule in the working contract is absolute — never log,
// return or persist an API key — and a debug file is a place somebody
// eventually attaches to a ticket.
var redactedHeaders = []string{"authorization", "api-key", "x-api-key", "cookie", "proxy-authorization"}

// Transport tees the outbound request body to a file and forwards the request
// untouched.
type Transport struct {
	Base http.RoundTripper
	dir  string
	seq  atomic.Uint64
}

// New returns a Transport writing into dir, or base unchanged when dir is
// empty — so the wiring can be unconditional and the switch is one env var.
//
// A directory that cannot be created disables the tap rather than failing the
// boot. This is a debug facility: a deployment that cannot write to the path
// somebody typed should still serve turns.
func New(base http.RoundTripper, dir string) http.RoundTripper {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return base
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		logrus.WithError(err).WithField("dir", dir).
			Error("llm wire tap: the directory could not be created; the tap is off and turns are unaffected")
		return base
	}
	logrus.WithField("dir", dir).
		Warn("llm wire tap: ON — every outbound prompt is being written to disk, including tenant data; unset LLM_WIRE_TAP_DIR when the capture is finished")
	return &Transport{Base: base, dir: dir}
}

// RoundTrip writes the request and forwards it.
//
// **The write happens before the call and its failure is never returned.** The
// question this answers is what the provider was sent, so a capture that could
// fail a turn would be a debug switch that changes the behaviour it was turned
// on to observe — which is the whole complaint against the proxy.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	body, err := t.replayBody(req)
	if err != nil {
		logrus.WithError(err).Debug("llm wire tap: the request body could not be read; forwarding it untouched")
		return base.RoundTrip(req)
	}
	t.write(req, body)
	return base.RoundTrip(req)
}

// replayBody reads the body and puts a fresh reader back, because a request
// whose body has been consumed is a request the real transport sends empty.
func (t *Transport) replayBody(req *http.Request) ([]byte, error) {
	if req == nil || req.Body == nil {
		return nil, nil
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(raw))
	// GetBody is what the stdlib uses to replay a request on a redirect or a
	// retried HTTP/2 stream. Leaving the old one in place would replay the
	// reader this function just drained.
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }
	return raw, nil
}

// write puts one request in one file. The body is written verbatim rather than
// re-marshalled: what this row is evidence for is the bytes, and a round trip
// through a JSON encoder would reorder keys and re-escape strings — which is
// precisely the class of difference the capture exists to rule out.
func (t *Transport) write(req *http.Request, body []byte) {
	name := fmt.Sprintf("%s-%04d.http", time.Now().UTC().Format("20060102T150405.000"), t.seq.Add(1))
	path := filepath.Join(t.dir, name)

	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s\n", req.Method, req.URL.String())
	for key, values := range req.Header {
		for _, v := range values {
			fmt.Fprintf(&b, "%s: %s\n", key, redact(key, v))
		}
	}
	b.WriteString("\n")
	b.Write(body)

	if err := os.WriteFile(path, b.Bytes(), 0o640); err != nil {
		logrus.WithError(err).WithField("path", path).Debug("llm wire tap: writing the capture failed")
	}
}

// redact replaces a credential with a fixed marker rather than a truncation.
// A prefix of a bearer token is still a prefix of a bearer token, and the point
// of the file is a body nobody has to sanitise before reading.
func redact(key, value string) string {
	lower := strings.ToLower(key)
	for _, h := range redactedHeaders {
		if strings.Contains(lower, h) {
			return "[redacted]"
		}
	}
	return value
}
