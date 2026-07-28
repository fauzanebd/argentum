package webhookout

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeDeliveries is the delivery log, in memory.
type fakeDeliveries struct {
	mu   sync.Mutex
	rows map[string]*domain.WebhookDelivery
	seq  int
}

func newFakeDeliveries() *fakeDeliveries {
	return &fakeDeliveries{rows: map[string]*domain.WebhookDelivery{}}
}

func (f *fakeDeliveries) Create(_ context.Context, d *domain.WebhookDelivery) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	d.ID = "del-" + string(rune('0'+f.seq))
	d.CreatedAt = time.Unix(1_800_000_000, 0)
	cp := *d
	f.rows[d.ID] = &cp
	return nil
}

func (f *fakeDeliveries) Get(_ context.Context, id string) (*domain.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *row
	return &cp, nil
}

func (f *fakeDeliveries) RecordAttempt(_ context.Context, id string, status domain.WebhookDeliveryStatus, httpStatus int, errMsg string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return domain.ErrNotFound
	}
	row.Attempts++
	row.Status = status
	row.LastStatus = httpStatus
	row.LastError = errMsg
	if status == domain.WebhookDelivered {
		t := at
		row.DeliveredAt = &t
	}
	return nil
}

func (f *fakeDeliveries) ListByCompany(context.Context, string, int) ([]*domain.WebhookDelivery, error) {
	return nil, nil
}

type fixedSecret struct{}

func (fixedSecret) EnsureWebhookSecret(context.Context, string) (string, error) {
	return testSecret, nil
}

type recordingDispatcher struct{ ids []string }

func (r *recordingDispatcher) EnqueueWebhookDelivery(_ context.Context, id string) error {
	r.ids = append(r.ids, id)
	return nil
}

// The receiver's half, end to end: what we send verifies against the secret we
// told them about, using the bytes exactly as they arrived on the wire.
func TestDeliveredBodyVerifiesAtTheReceiver(t *testing.T) {
	var gotSig, gotEvent string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get(SignatureHeader)
		gotEvent = r.Header.Get(EventHeader)
		// io.ReadAll, not a sized Read: a short read would hand the verifier a
		// truncated body and this test would be asserting that a mangled
		// payload fails to verify, which it would do for the wrong reason.
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	log := newFakeDeliveries()
	dispatch := &recordingDispatcher{}
	sender := NewSender(log, fixedSecret{}, dispatch, true)

	id, err := sender.Send(context.Background(), "co-1", "report.completed", srv.URL,
		map[string]any{"event": "report.completed", "data": map[string]string{"id": "rep_1"}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(dispatch.ids) != 1 || dispatch.ids[0] != id {
		t.Fatalf("dispatched %v, want [%s]", dispatch.ids, id)
	}

	if err := NewDeliverer(log, fixedSecret{}, true, 5).Deliver(context.Background(), id); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if gotEvent != "report.completed" {
		t.Errorf("%s = %q", EventHeader, gotEvent)
	}
	if err := Verify(testSecret, gotSig, gotBody, time.Now(), 0); err != nil {
		t.Fatalf("the receiver could not verify what we sent: %v", err)
	}
	// One byte different and it must not.
	tampered := append([]byte{}, gotBody...)
	tampered[len(tampered)-2] ^= 0x20
	if err := Verify(testSecret, gotSig, tampered, time.Now(), 0); err == nil {
		t.Fatal("a tampered body verified")
	}

	row, _ := log.Get(context.Background(), id)
	if row.Status != domain.WebhookDelivered || row.Attempts != 1 || row.DeliveredAt == nil {
		t.Errorf("log row = %+v, want one delivered attempt with a timestamp", row)
	}
}

// A 4xx is the receiver saying no to this body, and it will say the same thing
// in ten minutes. A 5xx is the receiver being unwell. Only one of them is
// worth a retry, and getting it backwards means either hammering a server that
// rejected us or giving up on one that was redeploying.
func TestRetryPolicySplitsOnTheStatusClass(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantRetry  bool
		wantStatus domain.WebhookDeliveryStatus
	}{
		{"bad request", http.StatusBadRequest, false, domain.WebhookFailed},
		{"not found", http.StatusNotFound, false, domain.WebhookFailed},
		{"rate limited", http.StatusTooManyRequests, true, domain.WebhookPending},
		{"server error", http.StatusInternalServerError, true, domain.WebhookPending},
		{"bad gateway", http.StatusBadGateway, true, domain.WebhookPending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			log := newFakeDeliveries()
			sender := NewSender(log, fixedSecret{}, &recordingDispatcher{}, true)
			id, err := sender.Send(context.Background(), "co-1", "report.completed", srv.URL, map[string]string{"a": "b"})
			if err != nil {
				t.Fatalf("Send: %v", err)
			}

			err = NewDeliverer(log, fixedSecret{}, true, 5).Deliver(context.Background(), id)
			if tc.wantRetry != (err != nil) {
				t.Errorf("Deliver err = %v, wantRetry = %v", err, tc.wantRetry)
			}
			row, _ := log.Get(context.Background(), id)
			if row.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", row.Status, tc.wantStatus)
			}
			if row.LastStatus != tc.status {
				t.Errorf("last_status = %d, want %d", row.LastStatus, tc.status)
			}
		})
	}
}

// The budget has to stop somewhere, and the row has to say so — otherwise a
// tenant reading the log sees "pending" forever on a delivery asynq gave up on
// hours ago.
func TestDeliveryGivesUpAtTheRetryBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	log := newFakeDeliveries()
	sender := NewSender(log, fixedSecret{}, &recordingDispatcher{}, true)
	id, err := sender.Send(context.Background(), "co-1", "report.completed", srv.URL, map[string]string{"a": "b"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	d := NewDeliverer(log, fixedSecret{}, true, 3)

	for attempt := 1; attempt <= 3; attempt++ {
		err := d.Deliver(context.Background(), id)
		last := attempt == 3
		if last && err != nil {
			t.Errorf("attempt %d asked for another retry past the budget", attempt)
		}
		if !last && err == nil {
			t.Errorf("attempt %d did not ask for a retry", attempt)
		}
	}
	row, _ := log.Get(context.Background(), id)
	if row.Status != domain.WebhookFailed {
		t.Errorf("status = %q, want failed after the budget", row.Status)
	}
	if row.Attempts != 3 {
		t.Errorf("attempts = %d, want 3", row.Attempts)
	}
}

// A tenant cannot register a callback to our own network, and cannot get one
// delivered there either.
func TestSendRefusesAPrivateTarget(t *testing.T) {
	log := newFakeDeliveries()
	sender := NewSender(log, fixedSecret{}, &recordingDispatcher{}, false)
	if _, err := sender.Send(context.Background(), "co-1", "report.completed",
		"http://169.254.169.254/latest/meta-data/", map[string]string{}); err == nil {
		t.Fatal("Send accepted the cloud metadata endpoint")
	}
	if len(log.rows) != 0 {
		t.Error("a refused target still wrote a delivery row")
	}
}

// A record already delivered is not delivered twice. asynq can re-run a task
// whose handler succeeded but whose acknowledgement was lost, and a tenant
// receiving two `report.completed` for one report has to deduplicate something
// we could have deduplicated first.
func TestDeliverIsIdempotentOnAnAlreadyDeliveredRow(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	log := newFakeDeliveries()
	sender := NewSender(log, fixedSecret{}, &recordingDispatcher{}, true)
	id, err := sender.Send(context.Background(), "co-1", "report.completed", srv.URL, map[string]string{"a": "b"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	d := NewDeliverer(log, fixedSecret{}, true, 5)
	for range 3 {
		if err := d.Deliver(context.Background(), id); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
	}
	if hits != 1 {
		t.Errorf("receiver was called %d times, want 1", hits)
	}
}
