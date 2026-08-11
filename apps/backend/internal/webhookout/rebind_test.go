package webhookout

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-H15. CheckResolvedTarget resolved the host and refused the request unless
// every answer passed — and then the sender built a request from the same URL
// *string* and handed it to a plain http.Client, which resolved the name a
// second time. Between the two lookups the answer can change: a record with a
// short TTL that says 203.0.113.10 to the check and 169.254.169.254 to the dial
// defeats the guard entirely.
//
// A real DNS name cannot be asked to open that window on cue, which is why
// Deliverer takes a Resolver.

// rebindingResolver answers `first` once and `then` on every later lookup.
type rebindingResolver struct {
	mu    sync.Mutex
	host  string
	first []net.IP
	then  []net.IP
	calls int
}

func (r *rebindingResolver) LookupIP(_ context.Context, _, host string) ([]net.IP, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if host != r.host {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	r.calls++
	if r.calls == 1 {
		return r.first, nil
	}
	return r.then, nil
}

func ips(t *testing.T, vals ...string) []net.IP {
	t.Helper()
	out := make([]net.IP, 0, len(vals))
	for _, v := range vals {
		ip := net.ParseIP(v)
		if ip == nil {
			t.Fatalf("ParseIP(%q) = nil", v)
		}
		out = append(out, ip)
	}
	return out
}

// The end-to-end property: the request lands on the address the check
// approved, and the second answer is never consulted.
//
// allowPrivate is on so the approved address can be a loopback listener this
// test actually owns — the pin is what is under test, not which addresses are
// acceptable, and that half is covered below. Before T-H15 this same delivery
// went through http.DefaultTransport, which would have asked the *system*
// resolver for `rebind.test` and reached nothing at all.
func TestDeliverDialsTheAddressThatPassedTheCheck(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	res := &rebindingResolver{
		host:  "rebind.test",
		first: ips(t, "127.0.0.1"),
		then:  ips(t, "169.254.169.254"),
	}

	log := newFakeDeliveries()
	sender := NewSender(log, fixedSecret{}, &recordingDispatcher{}, true)
	id, err := sender.Send(context.Background(), "co-1", "report.completed",
		"http://rebind.test:"+port+"/hook", map[string]string{"a": "b"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if err := NewDeliverer(log, fixedSecret{}, true, 5).WithResolver(res).
		Deliver(context.Background(), id); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if hits != 1 {
		t.Fatalf("the receiver was called %d times, want 1 — the dial did not go to the checked address", hits)
	}
	if res.calls != 1 {
		t.Errorf("the resolver was asked %d times, want 1 — a second lookup is the window this ticket closes", res.calls)
	}
	row, _ := log.Get(context.Background(), id)
	if row.Status != domain.WebhookDelivered {
		t.Errorf("status = %q, want delivered", row.Status)
	}
}

// The check half still bites: a name whose *first* answer is the metadata
// endpoint is refused before anything is dialled.
func TestDeliverRefusesAnInwardFirstAnswer(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))

	// Registration cannot see this — the string is a name, and a name says
	// nothing about where it points — so the row exists and the refusal happens
	// at delivery.
	log := newFakeDeliveries()
	sender := NewSender(log, fixedSecret{}, &recordingDispatcher{}, false)
	id, err := sender.Send(context.Background(), "co-1", "report.completed",
		"https://rebind.test:"+port+"/hook", map[string]string{"a": "b"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	res := &rebindingResolver{
		host:  "rebind.test",
		first: ips(t, "203.0.113.10", "169.254.169.254"),
		then:  ips(t, "203.0.113.10"),
	}
	if err := NewDeliverer(log, fixedSecret{}, false, 5).WithResolver(res).
		Deliver(context.Background(), id); err != nil {
		t.Fatalf("Deliver asked for a retry on a refusal that will never change: %v", err)
	}
	if hits != 0 {
		t.Error("a refused target was dialled anyway")
	}
	row, _ := log.Get(context.Background(), id)
	if row.Status != domain.WebhookFailed {
		t.Errorf("status = %q, want failed", row.Status)
	}
	if !strings.Contains(row.LastError, "link-local") {
		t.Errorf("last_error = %q, want it to name what was refused", row.LastError)
	}
}

// The dialer, on its own. This is the piece that makes the pin real: whatever
// name it is handed, it connects to one of the addresses it was built with and
// to nothing else.
func TestPinnedDialIgnoresTheHostItIsAskedFor(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Buffered, so the accept loop never blocks and a late arrival is still
	// recorded rather than dropped. Reading it is how the test learns where the
	// dialer went; sampling a counter raced with the accept goroutine.
	accepts := make(chan struct{}, 8)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepts <- struct{}{}
			_ = conn.Close()
		}
	}()

	_, port, _ := net.SplitHostPort(ln.Addr().String())

	// Assert on where the dialer *went*, never on whether the pinned address
	// happened to answer. An earlier version of this test pinned 203.0.113.10
	// and read "the dial failed" as proof the pin held; TEST-NET-3 is only
	// unreachable by convention, and on a network with a hijacking resolver it
	// answers, so the test failed while the code was correct.

	// Asked for the listener, pinned elsewhere: the listener must never be
	// reached. Whether the pin itself answers is the network's business.
	dial := pinnedDial(ips(t, "203.0.113.10"), false)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	if conn, err := dial(ctx, "tcp", "127.0.0.1:"+port); err == nil {
		_ = conn.Close()
	}
	select {
	case <-accepts:
		t.Error("the listener was reached; the dialer consulted the host it was asked for")
	case <-time.After(200 * time.Millisecond):
	}

	// The other direction, so the test cannot pass by dialling nothing at all:
	// pinned to the listener and asked for a host that is not it, the pin is
	// what gets connected to.
	dial = pinnedDial(ips(t, "127.0.0.1"), true)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()

	conn, err := dial(ctx2, "tcp", "203.0.113.10:"+port)
	if err != nil {
		t.Fatalf("the pinned address was not dialled: %v", err)
	}
	_ = conn.Close()
	select {
	case <-accepts:
	case <-time.After(2 * time.Second):
		t.Error("the listener was never reached; the pin was not applied")
	}
}

// And it re-validates. Redundant on every path that reaches it today, and
// deliberately so: this hook sees the address the stack is about to connect to,
// so it is the last place a mistake above it can still be stopped.
func TestPinnedDialRevalidatesTheAddress(t *testing.T) {
	dial := pinnedDial(ips(t, "169.254.169.254"), false)
	if _, err := dial(context.Background(), "tcp", "metadata.test:80"); err == nil {
		t.Fatal("the dialer connected to a link-local pin")
	} else if !strings.Contains(err.Error(), "link-local") {
		t.Errorf("err = %v, want it to name what was refused", err)
	}

	// allowPrivate is the deployment saying it trusts its tenants with its own
	// network, and it still holds here — otherwise the development stack could
	// not deliver to the localhost receiver the gate uses.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	conn, err := pinnedDial(ips(t, "127.0.0.1"), true)(context.Background(), "tcp", "anything.test:"+port)
	if err != nil {
		t.Fatalf("allowPrivate dial: %v", err)
	}
	_ = conn.Close()
}

// ResolveTarget hands back what it approved, which is the signature change the
// fix turns on — a verdict alone cannot be dialled.
func TestResolveTargetReturnsTheApprovedAddresses(t *testing.T) {
	res := &rebindingResolver{
		host:  "hooks.test",
		first: ips(t, "203.0.113.10", "203.0.113.11"),
		then:  ips(t, "169.254.169.254"),
	}
	_, addrs, err := ResolveTarget(context.Background(), "https://hooks.test/hook", false, res)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if len(addrs) != 2 || !addrs[0].Equal(net.ParseIP("203.0.113.10")) {
		t.Fatalf("addrs = %v, want both approved answers", addrs)
	}

	// An IP literal needs no lookup at all, and must not consume one — the
	// resolver here answers for a different name and would error.
	_, addrs, err = ResolveTarget(context.Background(), "https://203.0.113.10/hook", false, res)
	if err != nil || len(addrs) != 1 {
		t.Fatalf("ResolveTarget(literal) = %v, %v", addrs, err)
	}

	// A name that does not resolve fails; a callback to it could not be
	// delivered either way.
	if _, _, err := ResolveTarget(context.Background(), "https://elsewhere.test/hook", false, res); err == nil {
		t.Error("an unresolvable name passed")
	} else if !strings.Contains(err.Error(), "does not resolve") {
		t.Errorf("err = %v, want it to say the name did not resolve", err)
	}
}
