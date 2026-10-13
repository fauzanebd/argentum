package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/email"
)

// RIZ-83: the top-up service against an in-memory repository that keeps the
// rules 090's indexes and the repository's guarded writes keep. The statements
// themselves are proven against Postgres in the scratch arm
// (adapters/postgres/scratch_topups_test.go).

// memTopups is a TopupRepository with the open-row unique indexes and the
// status guards of the real one.
type memTopups struct {
	mu      sync.Mutex
	rows    map[string]*domain.Topup
	seq     int
	failAt  string // "attach" makes AttachProof fail after the upload
	credits map[string]int64
}

func newMemTopups() *memTopups {
	return &memTopups{rows: map[string]*domain.Topup{}, credits: map[string]int64{}}
}

func copyTopup(t *domain.Topup) *domain.Topup { c := *t; return &c }

func (m *memTopups) Create(_ context.Context, t *domain.Topup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rows {
		if !r.Status.Open() {
			continue
		}
		if r.CompanyID == t.CompanyID {
			return domain.ErrTopupInProgress
		}
		if r.ExpectedAmountIDR == t.ExpectedAmountIDR {
			return domain.ErrTopupCodeTaken
		}
	}
	m.seq++
	t.ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", m.seq)
	t.CreatedAt = time.Now()
	t.Status = domain.TopupStatusAwaitingPayment
	m.rows[t.ID] = copyTopup(t)
	return nil
}

func (m *memTopups) GetForCompany(_ context.Context, companyID, id string) (*domain.Topup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok || r.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	return copyTopup(r), nil
}

func (m *memTopups) ListForCompany(_ context.Context, companyID string, limit int) ([]*domain.Topup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*domain.Topup{}
	for _, r := range m.rows {
		if r.CompanyID == companyID {
			out = append(out, copyTopup(r))
		}
	}
	return out, nil
}

func (m *memTopups) AttachProof(_ context.Context, companyID, id string, p domain.TopupProof) (*domain.Topup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAt == "attach" {
		return nil, errors.New("connection reset")
	}
	r, ok := m.rows[id]
	if !ok || r.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	if !proofAccepted(r, time.Now()) {
		return nil, domain.ErrTopupClosed
	}
	now := time.Now()
	r.Status, r.ProofKey, r.ProofContentType, r.ProofSHA256 = domain.TopupStatusInReview, p.Key, p.ContentType, p.SHA256
	payee := p.PayeeID
	r.PayeeID, r.ProofUploadedAt = &payee, &now
	return copyTopup(r), nil
}

func (m *memTopups) Cancel(_ context.Context, companyID, id string) (*domain.Topup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok || r.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	if r.Status != domain.TopupStatusAwaitingPayment {
		return nil, domain.ErrTopupNotCancellable
	}
	r.Status = domain.TopupStatusCancelled
	return copyTopup(r), nil
}

func (m *memTopups) ExpireStale(context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, r := range m.rows {
		if r.Status == domain.TopupStatusAwaitingPayment && time.Now().After(r.ExpiresAt) {
			r.Status = domain.TopupStatusExpired
			n++
		}
	}
	return n, nil
}

func (m *memTopups) ListForReview(context.Context) ([]*domain.OperatorTopup, error) {
	return nil, nil
}
func (m *memTopups) ListReviewed(context.Context, int) ([]*domain.OperatorTopup, error) {
	return nil, nil
}

func (m *memTopups) GetAnyCompany(_ context.Context, id string) (*domain.Topup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return copyTopup(r), nil
}

func (m *memTopups) Approve(_ context.Context, id, reviewer string) (*domain.Topup, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return nil, 0, domain.ErrNotFound
	}
	if r.Status != domain.TopupStatusInReview {
		return nil, 0, domain.ErrTopupAlreadyReviewed
	}
	r.Status, r.ReviewedBy = domain.TopupStatusApproved, reviewer
	m.credits[r.CompanyID] += r.CreditMicroUSD
	return copyTopup(r), m.credits[r.CompanyID], nil
}

func (m *memTopups) Reject(_ context.Context, id, reviewer, reason string) (*domain.Topup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if r.Status != domain.TopupStatusInReview {
		return nil, domain.ErrTopupAlreadyReviewed
	}
	r.Status, r.ReviewedBy, r.RejectReason = domain.TopupStatusRejected, reviewer, &reason
	return copyTopup(r), nil
}

func (m *memTopups) setExpiry(id string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[id].ExpiresAt = at
}

// memStore is a TopupProofStore.
type memStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
	putErr  error
	removed []string
}

func newMemStore() *memStore {
	return &memStore{objects: map[string][]byte{}, types: map[string]string{}}
}

func (s *memStore) UploadKey(_ context.Context, key string, r io.Reader, ct string) (string, error) {
	if s.putErr != nil {
		return "", s.putErr
	}
	b, _ := io.ReadAll(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key], s.types[key] = b, ct
	return "https://storage.example/" + key, nil
}

func (s *memStore) RemoveKey(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	s.removed = append(s.removed, key)
	return nil
}

func (s *memStore) PresignKey(_ context.Context, key string, ttl time.Duration) (string, error) {
	return fmt.Sprintf("https://storage.example/%s?X-Amz-Expires=%d", key, int(ttl.Seconds())), nil
}

type countingInvalidator struct{ companies []string }

func (c *countingInvalidator) InvalidateBudget(_ context.Context, companyID string) {
	c.companies = append(c.companies, companyID)
}

// Obvious fakes. The real accounts are deploy config and never in this repo.
var testPayees = []domain.TopupPayee{
	{ID: "bri", Label: "BRI", AccountNumber: "000000000000001", AccountName: "Payee Name"},
	{ID: "jenius", Label: "Jenius", AccountNumber: "00000000002", AccountName: "Payee Name"},
}

func testSettings() TopupSettings {
	return TopupSettings{MinIDR: 20_000, MaxIDR: 5_000_000, PresetsIDR: []int64{50_000, 100_000, 250_000}, Payees: testPayees}
}

// codes returns a code source that yields seq, then 900, 901, ….
func codes(seq ...int) func() int {
	var mu sync.Mutex
	i, next := 0, 900
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		if i < len(seq) {
			i++
			return seq[i-1]
		}
		next++
		return next
	}
}

type topupRig struct {
	svc    *TopupService
	repo   *memTopups
	store  *memStore
	src    *stubFX
	budget *countingInvalidator
}

func newTopupRig(t *testing.T) *topupRig {
	t.Helper()
	src := &stubFX{quote: marketQuote(16_000)}
	rig := &topupRig{repo: newMemTopups(), store: newMemStore(), src: src, budget: &countingInvalidator{}}
	rig.svc = NewTopupService(rig.repo, NewTopupRates(src, nil, ratePolicy()), testSettings()).
		WithProofStore(rig.store).
		WithBudget(rig.budget).
		withCodeSource(codes()).
		withSyncAlerts()
	return rig
}

func pngBytes(size int) []byte {
	b := make([]byte, size)
	copy(b, "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	return b
}

// AC1: the credit, the frozen rate and the code, all decided here.
func TestTopupCreateFreezesTheRateAndTheCredit(t *testing.T) {
	rig := newTopupRig(t)
	before := time.Now()
	tp, err := rig.svc.Create(context.Background(), "co-a", "user-a", 150_000)
	if err != nil {
		t.Fatal(err)
	}
	if tp.AmountIDR != 150_000 || tp.CreditMicroUSD != 8_928_571 || tp.Status != domain.TopupStatusAwaitingPayment {
		t.Errorf("created %+v", tp)
	}
	if tp.FX.Source != domain.TopupFXSourceMarket || *tp.FX.MarketIDRPerUSDE4 != 160_000_000 ||
		*tp.FX.SpreadBP != 500 || tp.FX.EffectiveIDRPerUSDE4 != 168_000_000 || tp.FX.RateDate != "2026-10-12" {
		t.Errorf("froze %+v", tp.FX)
	}
	if tp.UniqueCode < 1 || tp.UniqueCode > 999 || tp.ExpectedAmountIDR != 150_000+int64(tp.UniqueCode) {
		t.Errorf("code %d, expected amount %d", tp.UniqueCode, tp.ExpectedAmountIDR)
	}
	if d := tp.ExpiresAt.Sub(before); d < 48*time.Hour-time.Minute || d > 48*time.Hour+time.Minute {
		t.Errorf("expires %v after creation, want 48h", d)
	}
}

// The real random source stays in 1–999.
func TestRandomTopupCodeIsAThreeDigitCode(t *testing.T) {
	for i := 0; i < 2000; i++ {
		if c := randomTopupCode(); c < 1 || c > 999 {
			t.Fatalf("code %d", c)
		}
	}
}

// AC2: one open request per workspace.
func TestTopupCreateRefusesASecondOpenRequest(t *testing.T) {
	rig := newTopupRig(t)
	if _, err := rig.svc.Create(context.Background(), "co-a", "u", 100_000); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.svc.Create(context.Background(), "co-a", "u", 50_000); !errors.Is(err, domain.ErrTopupInProgress) {
		t.Fatalf("second request: %v, want ErrTopupInProgress", err)
	}
	if n := len(rig.repo.rows); n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
}

// AC3 / AC24 / Q34: a code another workspace's open request already uses is
// drawn again, and amounts that differ only by their code coexist.
func TestTopupCreateDrawsAnotherCodeOnACollision(t *testing.T) {
	rig := newTopupRig(t)
	rig.svc.withCodeSource(codes(347, 347, 512, 348))
	ctx := context.Background()

	a, err := rig.svc.Create(ctx, "co-a", "u", 100_000)
	if err != nil || a.ExpectedAmountIDR != 100_347 {
		t.Fatalf("A: %+v, %v", a, err)
	}
	b, err := rig.svc.Create(ctx, "co-b", "u", 100_000)
	if err != nil || b.ExpectedAmountIDR != 100_512 {
		t.Fatalf("B: %+v, %v; want Rp 100.512 after 347 collided", b, err)
	}
	c, err := rig.svc.Create(ctx, "co-c", "u", 150_000)
	if err != nil || c.ExpectedAmountIDR != 150_348 {
		t.Fatalf("C: %+v, %v", c, err)
	}
	rig.svc.withCodeSource(codes(347))
	d, err := rig.svc.Create(ctx, "co-d", "u", 150_000)
	if err != nil || d.ExpectedAmountIDR != 150_347 {
		t.Fatalf("D: %+v, %v; Rp 150.347 should sit beside Rp 150.348", d, err)
	}
	seen := map[int64]bool{}
	for _, r := range rig.repo.rows {
		if seen[r.ExpectedAmountIDR] {
			t.Errorf("two open requests for Rp %d", r.ExpectedAmountIDR)
		}
		seen[r.ExpectedAmountIDR] = true
	}
}

// Twenty misses is a broken source, and it answers "not available".
func TestTopupCreateGivesUpAfterTwentyCollisions(t *testing.T) {
	rig := newTopupRig(t)
	rig.svc.withCodeSource(func() int { return 347 })
	if _, err := rig.svc.Create(context.Background(), "co-a", "u", 100_000); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.svc.Create(context.Background(), "co-b", "u", 100_000); !errors.Is(err, ErrTopupUnavailable) {
		t.Fatalf("got %v, want ErrTopupUnavailable", err)
	}
}

// AC22: the server checks every amount, and the message names the bound.
func TestTopupAmountValidation(t *testing.T) {
	rig := newTopupRig(t)
	const (
		minMsg   = "The minimum top-up is Rp 20.000."
		maxMsg   = "The maximum top-up is Rp 5.000.000."
		wholeMsg = "Enter an amount in whole thousands of rupiah."
	)
	for _, c := range []struct {
		raw  string // the JSON value of amount_idr; "" is a missing field
		want string // "" is accepted
	}{
		{`19000`, minMsg},
		{`5001000`, maxMsg},
		{`150500`, wholeMsg},
		{`"150000"`, wholeMsg},
		{``, wholeMsg},
		{`null`, wholeMsg},
		{`150000.5`, wholeMsg},
		{`1.5e5`, wholeMsg},
		{`-150000`, minMsg},
		{`99999999999999999999`, maxMsg},
		{`20000`, ""},
		{`5000000`, ""},
	} {
		amount, err := rig.svc.ParseTopupAmount(json.RawMessage(c.raw))
		if err == nil {
			err = rig.svc.validateAmount(amount)
		}
		var ae *TopupAmountError
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s refused: %v", c.raw, err)
		case c.want != "" && (!errors.As(err, &ae) || ae.Message != c.want):
			t.Errorf("%s: %v, want %q", c.raw, err, c.want)
		case c.want != "" && !errors.Is(err, domain.ErrInvalidInput):
			t.Errorf("%s: the amount error does not wrap ErrInvalidInput", c.raw)
		}
	}
	if _, err := rig.svc.Create(context.Background(), "co-a", "u", 19_000); err == nil {
		t.Error("Create accepted Rp 19.000")
	}
	if len(rig.repo.rows) != 0 {
		t.Errorf("a refused amount wrote %d rows", len(rig.repo.rows))
	}
}

// AC13: with no payees, nothing can be requested and nothing is fetched.
func TestTopupOffAnswersNotAvailable(t *testing.T) {
	src := &stubFX{quote: marketQuote(16_000)}
	settings := testSettings()
	settings.Payees = nil
	svc := NewTopupService(newMemTopups(), NewTopupRates(src, nil, ratePolicy()), settings)

	o := svc.Options(context.Background())
	if o.Enabled || len(o.Payees) != 0 || o.PreviewIDRPerUSDE4 != 0 {
		t.Errorf("options while off: %+v", o)
	}
	if _, err := svc.Create(context.Background(), "co-a", "u", 100_000); !errors.Is(err, ErrTopupUnavailable) {
		t.Errorf("create while off: %v", err)
	}
	if n := src.calls.Load(); n != 0 {
		t.Errorf("fetched the rate %d times while off", n)
	}
}

// R3 / AC28: options carry the effective rate and nothing of how it was made.
func TestTopupOptionsCarryOnlyTheEffectiveRate(t *testing.T) {
	rig := newTopupRig(t)
	o := rig.svc.Options(context.Background())
	if !o.Enabled || o.MinIDR != 20_000 || o.MaxIDR != 5_000_000 || o.StepIDR != 1000 ||
		o.PreviewIDRPerUSDE4 != 168_000_000 || o.ExpiresAfterHours != 48 || len(o.Payees) != 2 {
		t.Errorf("options: %+v", o)
	}
	raw, _ := json.Marshal(o)
	for _, leak := range []string{"16000", "160000000", "spread", "market"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("options carry %q: %s", leak, raw)
		}
	}
}

// AC4: a proof is stored under the request's prefix and moves it to review.
func TestTopupUploadProofMovesToReview(t *testing.T) {
	rig := newTopupRig(t)
	ctx := context.Background()
	tp, _ := rig.svc.Create(ctx, "co-a", "u", 100_000)

	got, err := rig.svc.UploadProof(ctx, ProofInput{
		CompanyID: "co-a", UserID: "u", TopupID: tp.ID, PayeeID: "bri", Body: bytes.NewReader(pngBytes(300 << 10)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TopupStatusInReview || got.ProofUploadedAt == nil || len(got.ProofSHA256) != 64 ||
		got.PayeeID == nil || *got.PayeeID != "bri" {
		t.Errorf("after upload: %+v", got)
	}
	prefix := "topups/co-a/" + tp.ID + "/"
	if len(rig.store.objects) != 1 {
		t.Fatalf("%d objects stored", len(rig.store.objects))
	}
	for key, b := range rig.store.objects {
		if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, ".png") || len(b) != 300<<10 {
			t.Errorf("stored %s (%d bytes), want under %s", key, len(b), prefix)
		}
		if rig.store.types[key] != "image/png" {
			t.Errorf("stored as %s", rig.store.types[key])
		}
	}
	// A replaced proof keeps the first object: it is evidence too.
	if _, err := rig.svc.UploadProof(ctx, ProofInput{
		CompanyID: "co-a", TopupID: tp.ID, PayeeID: "jenius", Body: bytes.NewReader(pngBytes(1 << 10)),
	}); err != nil {
		t.Fatal(err)
	}
	if len(rig.store.objects) != 2 {
		t.Errorf("%d objects after a replacement, want 2", len(rig.store.objects))
	}
}

// AC5: every refusal before a byte is stored, the request left as it was.
func TestTopupUploadProofRefusals(t *testing.T) {
	text := []byte("Transfer berhasil. Ref 0000. This is a text file renamed .png")
	for _, c := range []struct {
		name   string
		payee  string
		body   []byte
		noRepo bool
		want   error
	}{
		{"7 MB", "bri", pngBytes(7 << 20), false, ErrTopupProofTooLarge},
		{"text renamed .png", "bri", text, false, ErrTopupProofType},
		{"unknown payee", "xyz", pngBytes(1 << 10), false, ErrTopupUnknownPayee},
		{"no storage", "bri", pngBytes(1 << 10), true, ErrTopupProofUnavailable},
		{"empty", "bri", nil, false, ErrTopupProofType},
	} {
		t.Run(c.name, func(t *testing.T) {
			rig := newTopupRig(t)
			if c.noRepo {
				rig.svc.WithProofStore(nil)
			}
			ctx := context.Background()
			tp, _ := rig.svc.Create(ctx, "co-a", "u", 100_000)
			_, err := rig.svc.UploadProof(ctx, ProofInput{CompanyID: "co-a", TopupID: tp.ID, PayeeID: c.payee, Body: bytes.NewReader(c.body)})
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if after, _ := rig.repo.GetForCompany(ctx, "co-a", tp.ID); after.Status != domain.TopupStatusAwaitingPayment {
				t.Errorf("status %s, want awaiting_payment", after.Status)
			}
			if len(rig.store.objects) != 0 {
				t.Errorf("%d objects left in storage", len(rig.store.objects))
			}
		})
	}
}

// Each accepted type, by its bytes.
func TestTopupUploadProofAcceptsEachType(t *testing.T) {
	for _, c := range []struct {
		head, ext string
	}{
		{"\x89PNG\r\n\x1a\n", "png"},
		{"\xFF\xD8\xFF\xE0", "jpg"},
		{"RIFF\x00\x00\x00\x00WEBPVP8 ", "webp"},
		{"%PDF-1.7\n", "pdf"},
	} {
		rig := newTopupRig(t)
		ctx := context.Background()
		tp, _ := rig.svc.Create(ctx, "co-a", "u", 100_000)
		body := append([]byte(c.head), make([]byte, 600)...)
		if _, err := rig.svc.UploadProof(ctx, ProofInput{CompanyID: "co-a", TopupID: tp.ID, PayeeID: "bri", Body: bytes.NewReader(body)}); err != nil {
			t.Errorf("%s refused: %v", c.ext, err)
			continue
		}
		for key := range rig.store.objects {
			if !strings.HasSuffix(key, "."+c.ext) {
				t.Errorf("%s stored as %s", c.ext, key)
			}
		}
	}
}

// R5: a row that will not take the key removes the object it would point at.
func TestTopupUploadProofRemovesTheObjectWhenTheRowFails(t *testing.T) {
	rig := newTopupRig(t)
	ctx := context.Background()
	tp, _ := rig.svc.Create(ctx, "co-a", "u", 100_000)
	rig.repo.failAt = "attach"
	if _, err := rig.svc.UploadProof(ctx, ProofInput{CompanyID: "co-a", TopupID: tp.ID, PayeeID: "bri", Body: bytes.NewReader(pngBytes(1 << 10))}); err == nil {
		t.Fatal("upload succeeded with a failing row")
	}
	if len(rig.store.objects) != 0 || len(rig.store.removed) != 1 {
		t.Errorf("objects %d, removed %v", len(rig.store.objects), rig.store.removed)
	}
}

// AC6: another workspace's id is not found, for a proof and for a cancel.
func TestTopupIsScopedToItsCompany(t *testing.T) {
	rig := newTopupRig(t)
	ctx := context.Background()
	a, _ := rig.svc.Create(ctx, "co-a", "u", 100_000)
	if _, err := rig.svc.UploadProof(ctx, ProofInput{CompanyID: "co-b", TopupID: a.ID, PayeeID: "bri", Body: bytes.NewReader(pngBytes(1 << 10))}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("B's proof on A's top-up: %v", err)
	}
	if _, err := rig.svc.Cancel(ctx, "co-b", a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("B's cancel of A's top-up: %v", err)
	}
	if list, _ := rig.svc.List(ctx, "co-b"); len(list) != 0 {
		t.Errorf("B lists %d of A's top-ups", len(list))
	}
}

// AC11: an unpaid request past its expiry is listed expired, refuses proof,
// and frees the workspace for a new one.
func TestTopupExpiry(t *testing.T) {
	rig := newTopupRig(t)
	ctx := context.Background()
	old, _ := rig.svc.Create(ctx, "co-a", "u", 100_000)
	rig.repo.setExpiry(old.ID, time.Now().Add(-time.Minute))

	if _, err := rig.svc.UploadProof(ctx, ProofInput{CompanyID: "co-a", TopupID: old.ID, PayeeID: "bri", Body: bytes.NewReader(pngBytes(1 << 10))}); !errors.Is(err, domain.ErrTopupClosed) {
		t.Errorf("proof on an expired request: %v, want ErrTopupClosed", err)
	}
	list, _ := rig.svc.List(ctx, "co-a")
	if len(list) != 1 || list[0].Status != domain.TopupStatusExpired {
		t.Errorf("list: %+v", list)
	}
	if _, err := rig.svc.Create(ctx, "co-a", "u", 100_000); err != nil {
		t.Errorf("a new request after expiry: %v", err)
	}
	if len(rig.store.objects) != 0 {
		t.Error("a refused proof was stored")
	}
}

// AC12: only a request waiting for payment can be cancelled.
func TestTopupCancel(t *testing.T) {
	rig := newTopupRig(t)
	ctx := context.Background()
	a, _ := rig.svc.Create(ctx, "co-a", "u", 100_000)
	if got, err := rig.svc.Cancel(ctx, "co-a", a.ID); err != nil || got.Status != domain.TopupStatusCancelled {
		t.Fatalf("cancel: %+v, %v", got, err)
	}
	b, _ := rig.svc.Create(ctx, "co-a", "u", 100_000)
	_, _ = rig.svc.UploadProof(ctx, ProofInput{CompanyID: "co-a", TopupID: b.ID, PayeeID: "bri", Body: bytes.NewReader(pngBytes(1 << 10))})
	if _, err := rig.svc.Cancel(ctx, "co-a", b.ID); !errors.Is(err, domain.ErrTopupNotCancellable) {
		t.Errorf("cancel in review: %v, want ErrTopupNotCancellable", err)
	}
}

// AC8 / AC9 / AC23: approval adds the frozen credit once and drops the cached
// verdict; a second approval and an approval after a reject are refused.
func TestTopupApproveAndReject(t *testing.T) {
	rig := newTopupRig(t)
	ctx := context.Background()
	a, _ := rig.svc.Create(ctx, "co-a", "u", 150_000)
	_, _ = rig.svc.UploadProof(ctx, ProofInput{CompanyID: "co-a", TopupID: a.ID, PayeeID: "bri", Body: bytes.NewReader(pngBytes(1 << 10))})

	// The market moves and the spread changes after the request was made.
	rig.src.quote = marketQuote(17_000)
	rig.svc.rates.policy.SpreadBP = 1000

	got, balance, err := rig.svc.Approve(ctx, a.ID, "operator-1")
	if err != nil || got.Status != domain.TopupStatusApproved || balance != 8_928_571 {
		t.Fatalf("approve: %+v, %d, %v; want the frozen 8928571", got, balance, err)
	}
	if len(rig.budget.companies) != 1 || rig.budget.companies[0] != "co-a" {
		t.Errorf("budget invalidated for %v, want [co-a]", rig.budget.companies)
	}
	if _, _, err := rig.svc.Approve(ctx, a.ID, "operator-2"); !errors.Is(err, domain.ErrTopupAlreadyReviewed) {
		t.Errorf("second approve: %v", err)
	}
	if rig.repo.credits["co-a"] != 8_928_571 {
		t.Errorf("credited %d, want once", rig.repo.credits["co-a"])
	}

	// A new request in another workspace uses the new rate (AC23).
	rig.svc.rates.cache.Store(ctx, FXQuote{}) // force a refetch
	b, _ := rig.svc.Create(ctx, "co-b", "u", 150_000)
	if b.FX.EffectiveIDRPerUSDE4 != 187_000_000 || b.CreditMicroUSD != 8_021_390 || *b.FX.SpreadBP != 1000 {
		t.Errorf("new request froze %+v credit %d", b.FX, b.CreditMicroUSD)
	}

	// AC10: a reason is required; a rejected top-up cannot then be approved.
	_, _ = rig.svc.UploadProof(ctx, ProofInput{CompanyID: "co-b", TopupID: b.ID, PayeeID: "jenius", Body: bytes.NewReader(pngBytes(1 << 10))})
	for _, empty := range []string{"", "   \n"} {
		if _, err := rig.svc.Reject(ctx, b.ID, "operator-1", empty); !errors.Is(err, ErrTopupReasonRequired) {
			t.Errorf("reject with %q: %v", empty, err)
		}
	}
	if _, err := rig.svc.Reject(ctx, b.ID, "operator-1", strings.Repeat("x", 301)); !errors.Is(err, ErrTopupReasonTooLong) {
		t.Errorf("reject with 301 characters: %v", err)
	}
	reason := "No transfer of Rp 50.019 reached our Jenius account."
	r, err := rig.svc.Reject(ctx, b.ID, "operator-1", "  "+reason+" ")
	if err != nil || r.Status != domain.TopupStatusRejected || *r.RejectReason != reason {
		t.Fatalf("reject: %+v, %v", r, err)
	}
	if _, _, err := rig.svc.Approve(ctx, b.ID, "operator-1"); !errors.Is(err, domain.ErrTopupAlreadyReviewed) {
		t.Errorf("approve after reject: %v", err)
	}
	if rig.repo.credits["co-b"] != 0 {
		t.Errorf("a rejected top-up credited %d", rig.repo.credits["co-b"])
	}
}

// R10: View proof is a five-minute link to the latest proof.
func TestTopupProofLink(t *testing.T) {
	rig := newTopupRig(t)
	ctx := context.Background()
	a, _ := rig.svc.Create(ctx, "co-a", "u", 100_000)
	if _, err := rig.svc.ProofLink(ctx, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("link before any proof: %v", err)
	}
	_, _ = rig.svc.UploadProof(ctx, ProofInput{CompanyID: "co-a", TopupID: a.ID, PayeeID: "bri", Body: bytes.NewReader(pngBytes(1 << 10))})
	link, err := rig.svc.ProofLink(ctx, a.ID)
	if err != nil || link.ExpiresIn != 300 || link.ContentType != "image/png" || !strings.Contains(link.URL, "X-Amz-Expires=300") {
		t.Errorf("link: %+v, %v", link, err)
	}
}

// AC18 / Q21: each operator gets one alert; a failing sender costs the
// upload nothing; no base URL sends nothing.
func TestTopupProofAlertsTheOperators(t *testing.T) {
	upload := func(t *testing.T, sender *recordingSender, base string) (*recordingSender, error) {
		t.Helper()
		rig := newTopupRig(t)
		rig.svc.WithMailer(NewTopupMailer(sender, []string{"owner@example.com", "second@example.com"}, base),
			fakeCompanyName{name: "Toko Segar"}, fakeInviter{emailAddr: "dina@tokosegar.example"})
		rig.svc.withCodeSource(codes(347))
		ctx := context.Background()
		tp, _ := rig.svc.Create(ctx, "co-a", "u", 100_000)
		_, err := rig.svc.UploadProof(ctx, ProofInput{CompanyID: "co-a", UserID: "u", TopupID: tp.ID, PayeeID: "bri", Body: bytes.NewReader(pngBytes(1 << 10))})
		return sender, err
	}

	sender, err := upload(t, &recordingSender{enabled: true}, "https://app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 2 {
		t.Fatalf("%d messages, want one per operator", len(sender.sent))
	}
	want := map[string]bool{"owner@example.com": true, "second@example.com": true}
	for _, m := range sender.sent {
		if len(m.To) != 1 || !want[m.To[0]] {
			t.Errorf("sent to %v", m.To)
		}
		delete(want, m.To[0])
		if m.Subject != "Top-up to review: Toko Segar · Rp 100.347" {
			t.Errorf("subject %q", m.Subject)
		}
		for _, line := range []string{
			"Toko Segar (dina@tokosegar.example) uploaded proof for a top-up.",
			"Amount: Rp 100.347 to BRI",
			"Credit: $5.95",
			"Review it: https://app.example.com/operator/top-ups",
		} {
			if !strings.Contains(m.Text, line) {
				t.Errorf("body is missing %q:\n%s", line, m.Text)
			}
		}
	}

	hook := logrustest.NewGlobal()
	defer hook.Reset()
	if _, err := upload(t, &recordingSender{enabled: true, err: errors.New("550 relay refused")}, "https://app.example.com"); err != nil {
		t.Fatalf("a failing sender failed the upload: %v", err)
	}
	warned := false
	for _, e := range hook.AllEntries() {
		if e.Message == "top-up review email failed; the operator page still lists it" {
			warned = true
			if len(e.Data) != 1 || e.Data["topup_id"] == nil {
				t.Errorf("the warning carries %v, want the top-up id and nothing else", e.Data)
			}
		}
	}
	if !warned {
		t.Error("a failed send logged nothing")
	}

	if s, _ := upload(t, &recordingSender{enabled: true}, ""); len(s.sent) != 0 {
		t.Errorf("sent %d with no APP_BASE_URL", len(s.sent))
	}
	if s, _ := upload(t, &recordingSender{enabled: false}, "https://app.example.com"); len(s.sent) != 0 {
		t.Errorf("sent %d with email disabled", len(s.sent))
	}
}

func TestFormatRupiahAndCredit(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1.000", 100347: "100.347", 5000000: "5.000.000"} {
		if got := FormatRupiah(n); got != want {
			t.Errorf("FormatRupiah(%d) = %q, want %q", n, got, want)
		}
	}
	for n, want := range map[int64]string{5_763_831: "5.76", 8_928_571: "8.92", 5_000_000: "5.00", 9_999: "0.00"} {
		if got := FormatCreditUSD(n); got != want {
			t.Errorf("FormatCreditUSD(%d) = %q, want %q", n, got, want)
		}
	}
}

var _ email.Sender = (*recordingSender)(nil)
