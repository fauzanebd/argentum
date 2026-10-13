// Command whatsapp-attachment is RIZ-85's Q8: it builds the form Twilio posts
// for a WhatsApp voice note, signs it the way Twilio does, and — only when
// asked with -post — sends it to a running API's /webhook/whatsapp.
//
// It exists because the signature is the webhook's whole authentication (T-H1),
// so "curl a form at it" is a 401, and hand-computing HMAC-SHA1 over a sorted
// parameter list in a shell is how a gate gets run against the wrong string.
//
// **A posted form makes the API send a real WhatsApp message**, through the
// Twilio credentials the API was started with, to the number in -from. That is
// the gate: the owner's own allowlisted phone receives the reply. The default is
// a dry run that prints the form and its signature and sends nothing.
//
//	TWILIO_AUTH_TOKEN=… go run ./scripts/live-gates/whatsapp-attachment \
//	    -from +62811… -url http://localhost:8080/webhook/whatsapp [-post]
//
// The auth token is read from the environment and never from a flag, so it does
// not land in shell history or a process listing.
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	target := flag.String("url", "http://localhost:8080/webhook/whatsapp", "the API's WhatsApp webhook")
	from := flag.String("from", "", "the allowlisted sender, e.g. +62811…; the reply goes to this phone")
	to := flag.String("to", os.Getenv("TWILIO_WHATSAPP_NUMBER"), "the deployment's WhatsApp number (TWILIO_WHATSAPP_NUMBER)")
	contentType := flag.String("content-type", "audio/ogg", "MediaContentType0; audio/ogg is a voice note")
	numMedia := flag.Int("num-media", 1, "NumMedia; 0 sends a message with no attachment")
	body := flag.String("body", "", "Body; empty is a voice note, text is a caption")
	post := flag.Bool("post", false, "send the form; without it nothing leaves this machine")
	flag.Parse()

	token := os.Getenv("TWILIO_AUTH_TOKEN")
	if token == "" || *from == "" {
		fmt.Fprintln(os.Stderr, "TWILIO_AUTH_TOKEN in the environment and -from are required")
		os.Exit(2)
	}

	form := voiceNoteForm(*from, *to, *body, *contentType, *numMedia)
	signed, err := signedURL(*target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	sig := signature(token, signed, form)

	fmt.Printf("POST %s\nsigned as %s\nX-Twilio-Signature: %s\n%s\n", *target, signed, sig, form.Encode())
	if !*post {
		fmt.Println("dry run: nothing sent; add -post to send it")
		return
	}

	req, err := http.NewRequest(http.MethodPost, *target, strings.NewReader(form.Encode()))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", sig)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	fmt.Printf("%s %s\n", resp.Status, out)
	if resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
}

// voiceNoteForm is the subset of Twilio's inbound form the handler reads, plus
// a media URL. The URL is a placeholder the handler never fetches; it is there
// so the gate can check it stays out of the API log, which is where the real
// one — a bearer link to the recording — would otherwise end up.
func voiceNoteForm(from, to, body, contentType string, numMedia int) url.Values {
	sid := "SM" + randomHex(16)
	f := url.Values{
		"MessageSid": {sid},
		"SmsSid":     {sid},
		"From":       {"whatsapp:" + strings.TrimPrefix(from, "whatsapp:")},
		"To":         {"whatsapp:" + strings.TrimPrefix(to, "whatsapp:")},
		"Body":       {body},
		"NumMedia":   {strconv.Itoa(numMedia)},
	}
	if numMedia > 0 {
		f.Set("MediaContentType0", contentType)
		f.Set("MediaUrl0", "https://api.twilio.com/2010-04-01/Accounts/ACsynthetic/Messages/"+sid+"/Media/ME"+randomHex(16))
	}
	return f
}

// signedURL is the URL the handler checks the signature against: always https,
// whatever this request goes over, because the handler fixes the scheme (a TLS
// terminator in front of it leaves no trace of one). A local stack is plain
// http on the wire and https in the signed string.
func signedURL(target string) (string, error) {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("-url %q is not an absolute URL", target)
	}
	return "https://" + u.Host + u.RequestURI(), nil
}

// signature is Twilio's: HMAC-SHA1, keyed with the auth token, over the URL and
// then every parameter name followed by its value, names sorted.
func signature(authToken, signedURL string, form url.Values) string {
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	mac := hmac.New(sha1.New, []byte(authToken))
	mac.Write([]byte(signedURL))
	for _, k := range keys {
		for _, v := range form[k] {
			mac.Write([]byte(k))
			mac.Write([]byte(v))
		}
	}
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}
