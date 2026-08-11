package webhookout

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// CheckTarget refuses a callback URL we should not be pointed at, without
// touching the network.
//
// The URL is chosen by the caller, and an outbound POST to an address only our
// network can reach is server-side request forgery — the cloud metadata
// endpoint at 169.254.169.254 hands out instance credentials to anything that
// asks from inside the VPC, and "anything" would include us, on a tenant's
// behalf, with the result written into a delivery log they can read.
//
// This is the registration-time check, and it deliberately stops at what the
// string itself says: scheme, credentials, and the address rules when the host
// is an IP literal. A name is not resolved here, because a DNS hiccup would
// then reject a perfectly good `callback_url` with a 400 the caller cannot act
// on — and the check that actually protects anything is the one nearest the
// request. See CheckResolvedTarget.
//
// allowPrivate exists for local development and for the ticket's own gate,
// where the receiver is a script on localhost. It defaults to false everywhere
// else, and a deployment that turns it on has decided to trust its tenants
// with its internal network.
func CheckTarget(raw string, allowPrivate bool) error {
	_, err := parseTarget(raw, allowPrivate)
	return err
}

// Resolver is the name lookup this package uses. `*net.Resolver` satisfies it
// as written, which is the point: production passes net.DefaultResolver, and a
// test passes one that answers differently on the second call — the only way to
// demonstrate a rebinding window, since no real DNS name can be asked to open
// one on cue.
type Resolver interface {
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
}

// ResolveTarget is CheckTarget plus DNS, and is what the sender uses
// immediately before making the request. It hands back the addresses that
// passed so the caller can dial one of *them* rather than resolving again.
//
// Returning the addresses is the whole of T-H15. The check and the dial used to
// be two independent resolutions — CheckResolvedTarget asked, then
// `http.Client` asked again from the same URL string — and a record with a
// short TTL that answers publicly to the first question and 169.254.169.254 to
// the second walks past a guard that only ever saw the first answer.
//
// Every answer has to pass, not merely the first: we do not choose which
// address the dialer picks, and a name that resolves to both a public address
// and 169.254.169.254 is the shape of an attack rather than a coincidence.
// A name that does not resolve fails here — correctly, since a callback to it
// could not be delivered anyway.
//
// allowPrivate relaxes which addresses are acceptable; it does not skip the
// resolution. The pin is what makes the dial predictable, and a development
// deployment wants that property as much as a production one does.
func ResolveTarget(ctx context.Context, raw string, allowPrivate bool, res Resolver) (*url.URL, []net.IP, error) {
	u, err := parseTarget(raw, allowPrivate)
	if err != nil {
		return nil, nil, err
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		// A literal has nothing to re-resolve into, and parseTarget has already
		// run it past checkIP.
		return u, []net.IP{ip}, nil
	}
	if res == nil {
		res = net.DefaultResolver
	}
	addrs, err := res.LookupIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return nil, nil, fmt.Errorf("callback_url host does not resolve")
	}
	if !allowPrivate {
		for _, ip := range addrs {
			if err := checkIP(ip); err != nil {
				return nil, nil, err
			}
		}
	}
	return u, addrs, nil
}

// CheckResolvedTarget is ResolveTarget for a caller that wants only the verdict.
func CheckResolvedTarget(raw string, allowPrivate bool) error {
	_, _, err := ResolveTarget(context.Background(), raw, allowPrivate, nil)
	return err
}

// pinnedDial returns a DialContext that connects to one of addrs and to nothing
// else, whatever the name resolves to by the time the dial happens.
//
// This is the second half of T-H15: ResolveTarget decides the address, and
// without this the standard library resolves the name a second time inside
// http.Transport, so the decision would apply to an answer nobody dialled. The
// port is taken from the address the transport asked for — that comes from the
// URL, which was already parsed — and the host in it is discarded.
//
// checkIP runs again here, which is redundant on every path that reaches it
// today and deliberately so: this hook sees the address the stack is actually
// about to connect to, so it is the last place a mistake anywhere above can
// still be stopped.
func pinnedDial(addrs []net.IP, allowPrivate bool) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("callback target %q has no port", addr)
		}
		lastErr := error(fmt.Errorf("callback_url has no address left to try"))
		for _, ip := range addrs {
			if !allowPrivate {
				if err := checkIP(ip); err != nil {
					lastErr = err
					continue
				}
			}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// parseTarget does the network-free half and hands back the parsed URL.
func parseTarget(raw string, allowPrivate bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("callback_url is not a URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		// Plain HTTP leaks the payload and, more importantly, lets anything on
		// the path rewrite it — the signature proves we sent *something*, not
		// that the receiver got what we sent, if the receiver never checks it.
		// Permitted only where private targets are, which is to say in
		// development.
		if !allowPrivate {
			return nil, fmt.Errorf("callback_url must be https")
		}
	default:
		return nil, fmt.Errorf("callback_url must be an http(s) URL")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("callback_url has no host")
	}
	if u.User != nil {
		// Credentials in the URL end up in our delivery log and in our access
		// logs. A receiver that needs authentication should read the signature.
		return nil, fmt.Errorf("callback_url must not embed credentials")
	}
	if allowPrivate {
		return u, nil
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return u, checkIP(ip)
	}
	return u, nil
}

// checkIP rejects everything that is not a public unicast address.
//
// The link-local range is called out separately in the message because
// 169.254.169.254 is the address this whole function exists for, and an
// operator reading the log line should not have to know that a metadata
// endpoint is link-local to understand what was refused.
func checkIP(ip net.IP) error {
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("callback_url resolves to a loopback address")
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		return fmt.Errorf("callback_url resolves to a link-local address (cloud metadata lives there)")
	case ip.IsPrivate():
		return fmt.Errorf("callback_url resolves to a private address")
	case ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast():
		return fmt.Errorf("callback_url resolves to a non-routable address")
	}
	return nil
}
