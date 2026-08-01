package mcp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The egress guard is T-M1's gate item rather than a hardening pass, because
// "we validated the URL" is the sentence every SSRF postmortem contains. These
// tests are the table that sentence is not allowed to be true of.

func TestCheckURLRejectsWhatItMust(t *testing.T) {
	g := Guard{}
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"plaintext http", "http://mcp.example.com/", "must be https"},
		{"loopback by name", "http://localhost:6379/", "must be https"},
		{"loopback literal", "https://127.0.0.1:6379/", "loopback"},
		{"loopback v6", "https://[::1]/", "loopback"},
		{"cloud metadata", "https://169.254.169.254/latest/meta-data/", "link-local"},
		{"rfc1918 ten", "https://10.0.0.5/mcp", "private"},
		{"rfc1918 172", "https://172.16.4.4/mcp", "private"},
		{"rfc1918 192", "https://192.168.1.1/mcp", "private"},
		{"unique local v6", "https://[fd00::1]/mcp", "private"},
		{"unspecified", "https://0.0.0.0/mcp", "routable"},
		{"v4-mapped loopback", "https://[::ffff:127.0.0.1]/mcp", "loopback"},
		{"no host", "https:///mcp", "no host"},
		{"not a url scheme", "ftp://mcp.example.com/", "not a supported URL scheme"},
		{"stdio, which is never allowed", "stdio://local/bin/server", "not a supported URL scheme"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := g.CheckURL(tc.url)
			if err == nil {
				t.Fatalf("%s was allowed", tc.url)
			}
			if !errors.Is(err, ErrEgressBlocked) {
				t.Errorf("error is not ErrEgressBlocked: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q — an admin has to know which rule they hit", err, tc.want)
			}
		})
	}
}

func TestCheckURLAllowsAPublicHTTPSEndpoint(t *testing.T) {
	if err := (Guard{}).CheckURL("https://mcp.example.com/v1/mcp"); err != nil {
		t.Fatalf("a public https URL was blocked: %v", err)
	}
}

// The development flag is the whole escape hatch, and it has to open both
// halves — a laptop MCP server is on http and on 127.0.0.1.
func TestAllowPrivateOpensLoopbackAndPlaintext(t *testing.T) {
	g := Guard{AllowPrivate: true}
	for _, u := range []string{"http://localhost:9000/mcp", "http://127.0.0.1:9000/mcp"} {
		if err := g.CheckURL(u); err != nil {
			t.Errorf("%s was blocked with AllowPrivate: %v", u, err)
		}
	}
}

// The one rule the development flag does not open. A laptop server lives on
// loopback; nothing lives on 169.254.169.254 except the credentials this guard
// exists to keep a tenant away from.
func TestAllowPrivateStillRefusesLinkLocal(t *testing.T) {
	g := Guard{AllowPrivate: true}
	for _, u := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"https://169.254.169.254/",
		"http://[fe80::1]/mcp",
	} {
		if err := g.CheckURL(u); err == nil {
			t.Errorf("%s was allowed under AllowPrivate", u)
		}
	}
}

// The finding this ticket's gate produced. A public name that resolves to
// loopback passes the string check by construction, and without the resolved
// check it would be stored as a server every request then refuses — a row that
// can never work, with the reason buried in a probe error.
func TestAPublicNameResolvingPrivatelyIsRefusedAtSaveTime(t *testing.T) {
	g := Guard{}
	// localtest.me is a public DNS name whose A record is 127.0.0.1. If it ever
	// stops resolving, this test's refusal message changes but its verdict does
	// not — "does not resolve" is a refusal too.
	if _, dnsErr := net.LookupIP("example.com"); dnsErr != nil {
		t.Skip("no DNS available")
	}
	err := g.CheckResolvedURL("https://localtest.me/mcp")
	if err == nil {
		t.Fatal("a name resolving to loopback was allowed")
	}
	if !errors.Is(err, ErrEgressBlocked) {
		t.Errorf("error is not ErrEgressBlocked: %v", err)
	}
	if !strings.Contains(err.Error(), "localtest.me") {
		t.Errorf("error does not name the host the admin typed: %v", err)
	}
}

func TestCheckResolvedURLAllowsAPublicName(t *testing.T) {
	// Skipped rather than failed without DNS: the assertion is about the guard's
	// verdict on a public answer, and a machine with no resolver has nothing to
	// say about that.
	if _, err := net.LookupIP("example.com"); err != nil {
		t.Skip("no DNS available")
	}
	if err := (Guard{}).CheckResolvedURL("https://example.com/mcp"); err != nil {
		t.Fatalf("a public name was blocked: %v", err)
	}
}

// The development flag skips resolution as well as the address rules — a
// laptop server is reached by a name that answers 127.0.0.1.
func TestAllowPrivateSkipsResolution(t *testing.T) {
	if err := (Guard{AllowPrivate: true}).CheckResolvedURL("http://localhost:9410/mcp"); err != nil {
		t.Errorf("localhost was blocked with AllowPrivate: %v", err)
	}
}

// Plaintext http, opted into by the operator, without opening the network. The
// two flags are separate because the two asks are: a tenant whose MCP server has
// no TLS is not asking to reach our Redis.
func TestAllowInsecureHTTPCarriesPlaintextButNotPrivateAddresses(t *testing.T) {
	g := Guard{AllowInsecureHTTP: true}
	if err := g.CheckURL("http://mcp.example.com/v1"); err != nil {
		t.Errorf("plaintext to a public host was blocked: %v", err)
	}
	for _, blocked := range []string{
		"http://127.0.0.1:9410/mcp",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/mcp",
	} {
		if err := g.CheckURL(blocked); err == nil {
			t.Errorf("%s was allowed by the plaintext flag alone", blocked)
		}
	}
}

// The check that actually enforces the rule. CheckURL can only read what the
// tenant typed; this one runs on the address the kernel is about to dial, which
// is what makes a DNS answer that differs between the two harmless.
func TestTheDialIsCheckedAndNotOnlyTheURL(t *testing.T) {
	// A hostname that resolves to loopback is exactly the public-name,
	// private-answer case. localhost is the reliable way to have one in a test
	// that cannot control DNS.
	client := Guard{}.HTTPClient("")
	_, err := client.Get("http://localhost:1/mcp")
	if err == nil {
		t.Fatal("a request to a loopback-resolving name succeeded")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Errorf("error does not name the rule that stopped it: %v", err)
	}
}

// A legitimate host that 302s to the metadata endpoint is the case the
// CheckRedirect hook exists for, and the one a save-time URL check cannot see.
func TestARedirectToAPrivateAddressIsBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, "https://169.254.169.254/latest/meta-data/", http.StatusFound)
			return
		}
		fmt.Fprintln(w, "ok")
	}))
	defer srv.Close()

	// The test server is itself on loopback, so the guard has to allow private
	// addresses for the first hop to happen at all — which makes this a test of
	// the redirect check specifically. The metadata address is link-local, not
	// private, so it is refused even under the development flag.
	client := Guard{AllowPrivate: true}.HTTPClient("")
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return Guard{}.CheckURL(req.URL.String())
	}
	_, err := client.Get(srv.URL + "/hop")
	if err == nil {
		t.Fatal("a redirect to 169.254.169.254 was followed")
	}
	if !strings.Contains(err.Error(), "link-local") {
		t.Errorf("error does not name the rule that stopped it: %v", err)
	}
}

// The production guard refuses the metadata endpoint at the redirect too, which
// is the same assertion without the development flag muddying it.
func TestTheRedirectHookIsTheProductionCheck(t *testing.T) {
	client := Guard{}.HTTPClient("")
	req, _ := http.NewRequest(http.MethodGet, "https://169.254.169.254/", nil)
	if err := client.CheckRedirect(req, nil); err == nil {
		t.Fatal("the redirect hook allowed the metadata endpoint")
	}
}

// The token rides on every request the SDK makes, not only the first — the SSE
// reconnect is a request we never write.
func TestTheBearerTokenIsAddedToEveryRequest(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		fmt.Fprintln(w, "ok")
	}))
	defer srv.Close()

	client := Guard{AllowPrivate: true}.HTTPClient("s3cr3t")
	for i := range 2 {
		res, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_ = res.Body.Close()
	}
	for i, got := range seen {
		if got != "Bearer s3cr3t" {
			t.Errorf("request %d carried %q, want the bearer token", i, got)
		}
	}
}

// No token, no header — a server that needs none must not be sent an empty
// bearer, which some gateways answer with a 401.
func TestNoTokenMeansNoAuthorizationHeader(t *testing.T) {
	var got string
	var present bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, present = r.Header.Get("Authorization"), r.Header.Values("Authorization") != nil
		fmt.Fprintln(w, "ok")
	}))
	defer srv.Close()

	res, err := Guard{AllowPrivate: true}.HTTPClient("").Get(srv.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = res.Body.Close()
	if present || got != "" {
		t.Errorf("Authorization header was sent without a token: %q", got)
	}
}

// checkIP is the list this guard is, so it is asserted directly as well as
// through a URL — a refactor that stopped calling it would otherwise pass
// everything above by accident.
func TestCheckIP(t *testing.T) {
	g := Guard{}
	blocked := []string{
		"127.0.0.1", "::1", "169.254.169.254", "fe80::1",
		"10.1.2.3", "172.20.0.1", "192.168.0.1", "fd12::3",
		"0.0.0.0", "224.0.0.1",
	}
	for _, s := range blocked {
		if err := g.checkIP(net.ParseIP(s)); err == nil {
			t.Errorf("%s was allowed", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111", "203.0.113.7"}
	for _, s := range allowed {
		if err := g.checkIP(net.ParseIP(s)); err != nil {
			t.Errorf("%s was blocked: %v", s, err)
		}
	}
}
