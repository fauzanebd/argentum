// Package mcp is Argentum as an MCP *client*: it reaches the tenant's own
// server, lists its tools, and (in T-M2) calls them.
//
// Everything in this package treats the URL as attacker-controlled input
// (locked decision 4). A tenant who types `http://169.254.169.254/` is reaching
// our infrastructure from inside our own network position, not theirs, and the
// same is true of a public hostname whose DNS answers with an RFC1918 address
// or a legitimate host that 302s to one. The guard below is why that is a
// rejected request rather than a credential leak, and it is this ticket's gate
// rather than a hardening pass somebody does later.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrEgressBlocked is every refusal this guard makes. Callers surface it as a
// 400 with the reason attached: the tenant typed the URL and is the only one
// who can fix it.
//
// `internal/webhookout.CheckTarget` is the prior art and a weaker check on
// purpose: it resolves the name and inspects the answers, which leaves the
// window between the lookup and the dial open. It is not shared with this
// package because T-M1 requires the address to be pinned for the request and
// every redirect re-checked, and because the two carry different payloads — a
// signed webhook we composed versus a bearer token for a system we do not own.
// If the pinned dialer here proves out, that is the direction webhookout should
// move, not the other way round.
var ErrEgressBlocked = errors.New("egress blocked")

// Guard decides which addresses this deployment may open a connection to.
//
// AllowPrivate is the development escape hatch and nothing more. It exists
// because the gate for this ticket, and any developer running an MCP server on
// their laptop, needs to reach 127.0.0.1 — and because the alternative is
// somebody commenting the check out and forgetting. It is off unless
// MCP_ALLOW_PRIVATE_EGRESS is set, and cmd/api refuses to honour it outside
// development.
type Guard struct {
	AllowPrivate bool
	// AllowInsecureHTTP permits a plaintext http:// MCP URL while keeping every
	// address rule in force. It is a separate switch from AllowPrivate on
	// purpose: "my server has no TLS" and "my server is inside your network"
	// are different asks, and a tenant with an http endpoint on a public
	// address should not have to be granted the second to get the first.
	//
	// What it costs is real and is the operator's to accept: the bearer token
	// for the tenant's system crosses the network in the clear, and anything on
	// the path can read a tool result before we do. Off by default, and the
	// boot logs say so when it is on.
	AllowInsecureHTTP bool
	// Timeout bounds a single request to the tenant's server. A server that
	// accepts a connection and then never answers must not hold a worker.
	Timeout time.Duration
}

// DefaultTimeout is what a probe gets when nothing else is configured. Long
// enough for a cold Lambda behind an MCP endpoint, short enough that an admin
// pressing Save gets an answer rather than a spinner.
const DefaultTimeout = 15 * time.Second

// CheckURL rejects a URL before anything is dialled.
//
// This is the first of two checks and the weaker one: it can only see what the
// tenant typed. The address actually connected to is checked in Control, which
// is what makes a DNS answer that changes between the two harmless.
func (g Guard) CheckURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%w: %s is not a URL", ErrEgressBlocked, raw)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && (g.AllowPrivate || g.AllowInsecureHTTP):
		// Plaintext, deliberately allowed. AllowPrivate carries it for a
		// developer's laptop; AllowInsecureHTTP carries it for a deployment
		// whose tenants run MCP servers without TLS. Neither relaxes the
		// address rules below — an http URL still has to point somewhere
		// public unless AllowPrivate says otherwise.
	case u.Scheme == "http":
		return fmt.Errorf("%w: an MCP server URL must be https on this deployment", ErrEgressBlocked)
	default:
		return fmt.Errorf("%w: %q is not a supported URL scheme", ErrEgressBlocked, u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("%w: the URL has no host", ErrEgressBlocked)
	}
	// A literal address is decided here; a hostname is decided at dial time,
	// where the resolver's answer is known.
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if err := g.checkIP(ip); err != nil {
			return err
		}
	}
	return nil
}

// CheckResolvedURL is CheckURL plus DNS, and it is what a *save* asks.
//
// The distinction matters and cost this ticket's gate a finding: `localtest.me`
// is a public name that resolves to 127.0.0.1, so it passes the string check,
// and without this it would be stored as a server whose every request the dial
// check then refuses — a row that can never work, with the reason buried in a
// probe error. Rejecting it at save time is the sentence an admin can act on.
//
// Every answer has to pass, not merely the first: we do not choose which
// address the dialer picks, and a name that resolves to both a public address
// and 169.254.169.254 is the shape of an attack rather than a coincidence.
//
// A name that does not resolve is refused here too. That makes a DNS outage
// during a save a 400 the admin retries, which is the accepted cost of not
// storing an endpoint nothing can reach. Nothing else in the request path
// depends on this check — Control still runs on every dial — so a name that
// starts resolving privately *after* it was saved is caught there.
func (g Guard) CheckResolvedURL(raw string) error {
	if err := g.CheckURL(raw); err != nil {
		return err
	}
	if g.AllowPrivate {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%w: %s is not a URL", ErrEgressBlocked, raw)
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		// A literal was already decided by CheckURL.
		return nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("%w: %s does not resolve", ErrEgressBlocked, host)
	}
	for _, ip := range addrs {
		if err := g.checkIP(ip); err != nil {
			// The name is named as well as the address, because "localtest.me
			// is a loopback address" is the sentence that explains the refusal
			// to somebody who typed a hostname and expected a hostname back.
			return fmt.Errorf("%w: %s resolves to %s", ErrEgressBlocked, host, ip)
		}
	}
	return nil
}

// HTTPClient returns the only client this package makes requests with.
//
// Three things are bolted to it, and all three are load-bearing:
//
//  1. **Control**, which runs after the resolver and before the connect, on the
//     address the kernel is about to dial. That is what pins the answer: a
//     hostname that resolves publicly for the check and privately for the
//     connection is caught here, because there is no gap between the two.
//  2. **CheckRedirect**, which re-runs the whole URL check on every hop. "We
//     validated the URL" is the sentence in every SSRF postmortem, and a 302 to
//     `http://localhost:6379` from a legitimate host is how it gets written.
//  3. **A bearer token**, if the tenant set one, added by a RoundTripper rather
//     than by each call site — a header that has to be remembered is a header
//     somebody forgets.
func (g Guard) HTTPClient(token string) *http.Client {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("%w: %s is not an address", ErrEgressBlocked, address)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("%w: %s did not resolve to an address", ErrEgressBlocked, host)
			}
			return g.checkIP(ip)
		},
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		// No connection reuse across servers is needed here, and a small pool
		// keeps one tenant's slow server from holding file descriptors open
		// for the rest of them.
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
	}
	return &http.Client{
		Transport: &authTransport{base: transport, token: token},
		// No overall client timeout: the streamable transport holds a long-
		// lived SSE stream open on purpose, and a Client.Timeout would cut it.
		// The dial, handshake and response-header timeouts above are what bound
		// a server that accepts and then goes quiet.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("%w: too many redirects", ErrEgressBlocked)
			}
			return g.CheckURL(req.URL.String())
		},
	}
}

// checkIP is the address allowlist: public unicast only.
//
// Written as a list of refusals rather than "is it in 1.0.0.0/8 or …" because
// the interesting addresses are the ones with a name. 169.254.169.254 is the
// cloud metadata endpoint and the reason this function exists; loopback is the
// tenant reaching our Redis; the RFC1918 ranges are the rest of our network.
func (g Guard) checkIP(ip net.IP) error {
	// Link-local is refused even under AllowPrivate, and it is the one rule that
	// flag does not open. 169.254.0.0/16 is where the cloud metadata service
	// lives, it answers instance credentials to anything that asks, and no
	// developer has ever needed to reach it from an MCP server. The flag exists
	// so a laptop server on loopback is reachable; extending it to the address
	// this entire file was written for would be a footgun with a friendly name.
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("%w: %s is a link-local address", ErrEgressBlocked, ip)
	}
	if g.AllowPrivate {
		return nil
	}
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("%w: %s is a loopback address", ErrEgressBlocked, ip)
	case ip.IsPrivate():
		return fmt.Errorf("%w: %s is a private address", ErrEgressBlocked, ip)
	case ip.IsUnspecified():
		return fmt.Errorf("%w: %s is not a routable address", ErrEgressBlocked, ip)
	case ip.IsMulticast(), ip.IsInterfaceLocalMulticast():
		return fmt.Errorf("%w: %s is a multicast address", ErrEgressBlocked, ip)
	case isUniqueLocal(ip):
		// fc00::/7, IPv6's answer to RFC1918. net.IP.IsPrivate covers it, but
		// only for addresses that are not IPv4-mapped — this is the belt to
		// that braces, and it costs one comparison.
		return fmt.Errorf("%w: %s is a unique-local address", ErrEgressBlocked, ip)
	}
	// An IPv4-mapped IPv6 address (::ffff:127.0.0.1) answers false to
	// IsLoopback on some paths, so the mapped form is re-checked as v4.
	if v4 := ip.To4(); v4 != nil && !ip.Equal(v4) {
		return g.checkIP(v4)
	}
	return nil
}

func isUniqueLocal(ip net.IP) bool {
	v6 := ip.To16()
	return ip.To4() == nil && v6 != nil && v6[0]&0xfe == 0xfc
}

// authTransport adds the tenant's bearer token, if there is one.
//
// It is a RoundTripper rather than a header set by each caller because the SDK
// makes requests we do not write — the SSE stream reconnect most of all — and a
// token that only rides on the first request is a session that dies quietly.
type authTransport struct {
	base  http.RoundTripper
	token string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.token == "" {
		return t.base.RoundTrip(req)
	}
	// Cloned: the caller's request may be retried by the transport, and
	// mutating a request the SDK still holds is how a header ends up on the
	// wrong connection.
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

// contextWithTimeout bounds one probe. Exported behaviour lives on Client; this
// is here so both the URL check and the dial share one notion of "too long".
func (g Guard) contextWithTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return context.WithTimeout(ctx, timeout)
}
