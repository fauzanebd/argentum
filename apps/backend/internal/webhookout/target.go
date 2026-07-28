package webhookout

import (
	"fmt"
	"net"
	"net/url"
	"strings"
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

// CheckResolvedTarget is CheckTarget plus DNS, and is what the sender uses
// immediately before making the request.
//
// Every answer has to pass, not merely the first: we do not choose which
// address the dialer picks, and a name that resolves to both a public address
// and 169.254.169.254 is the shape of an attack rather than a coincidence.
// A name that does not resolve fails here — correctly, since a callback to it
// could not be delivered anyway.
func CheckResolvedTarget(raw string, allowPrivate bool) error {
	u, err := parseTarget(raw, allowPrivate)
	if err != nil {
		return err
	}
	if allowPrivate {
		return nil
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		// Already checked as a literal by parseTarget.
		return nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("callback_url host does not resolve")
	}
	for _, ip := range addrs {
		if err := checkIP(ip); err != nil {
			return err
		}
	}
	return nil
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
