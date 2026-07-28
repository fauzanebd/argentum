package webhookout

import "testing"

// The URL is chosen by the caller, so every one of these is a request somebody
// can make. 169.254.169.254 is the one that matters: on every major cloud it
// hands instance credentials to anything that asks from inside the VPC, and
// "anything" would include us, on a tenant's behalf.
func TestCheckTargetRefusesOurOwnNetwork(t *testing.T) {
	refused := []string{
		"http://169.254.169.254/latest/meta-data/iam/security-credentials/",
		"https://169.254.169.254/",
		"https://127.0.0.1/hook",
		"https://10.0.0.5/hook",
		"https://192.168.1.10/hook",
		"https://172.16.0.1/hook",
		"https://[::1]/hook",
		"http://example.com/hook",       // plain HTTP
		"ftp://example.com/hook",        // not HTTP at all
		"https://user:pw@example.com/h", // credentials in the URL
		"https:///hook",                 // no host
		"not a url at all",
	}
	for _, raw := range refused {
		if err := CheckTarget(raw, false); err == nil {
			t.Errorf("CheckTarget(%q) allowed it", raw)
		}
	}
}

func TestCheckTargetAllowsAPublicHTTPSTarget(t *testing.T) {
	if err := CheckTarget("https://hooks.example.com/argentum", false); err != nil {
		t.Fatalf("CheckTarget: %v", err)
	}
}

// The registration check does not resolve names, so a DNS hiccup cannot turn a
// good callback_url into a 400. The resolving check does, and `localhost` is
// the one name every machine answers for without a network — which makes it
// the only hostname this can be tested with offline.
func TestResolvedTargetCatchesANameThatPointsInward(t *testing.T) {
	// Registration lets the name through — it is a name, and nothing about the
	// string says where it points.
	if err := CheckTarget("https://localhost/hook", false); err != nil {
		t.Fatalf("the network-free check resolved a name: %v", err)
	}
	if err := CheckResolvedTarget("https://localhost/hook", false); err == nil {
		t.Fatal("a name resolving to loopback passed the resolving check")
	}
	if err := CheckResolvedTarget("https://no-such-host.invalid/hook", false); err == nil {
		t.Fatal("a name that does not resolve passed; it could not be delivered to either")
	}
}

// The development escape hatch, and the reason it is a config flag rather than
// a build tag: this ticket's own gate posts to a script on localhost.
func TestAllowPrivateOpensLoopback(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:9099/hook", "http://localhost:9099/hook"} {
		if err := CheckTarget(raw, true); err != nil {
			t.Errorf("CheckTarget(%q, allowPrivate) = %v", raw, err)
		}
	}
	// Still not a free-for-all: the scheme check is the one thing that stays.
	if err := CheckTarget("ftp://127.0.0.1/hook", true); err == nil {
		t.Error("allowPrivate admitted a non-HTTP scheme")
	}
}
