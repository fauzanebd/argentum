package skill

import (
	"context"
	"errors"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-K5's vectors. What is asserted here is that neither path can hurt anything
// — a failed embedding is a ranking a tenant does not get, never a save they do
// not get and never a turn without procedures.

type embedStore struct {
	*stubSkills
	pending []*domain.Skill
	stored  map[string][]float32
	// moved is the row an admin edited between the read and the write, which
	// is the race SetEmbedding's conditional write exists for.
	moved map[string]bool
}

func newEmbedStore(pending ...*domain.Skill) *embedStore {
	return &embedStore{
		stubSkills: &stubSkills{},
		pending:    pending,
		stored:     map[string][]float32{},
		moved:      map[string]bool{},
	}
}

func (e *embedStore) ListUnembedded(context.Context, string) ([]*domain.Skill, error) {
	return e.pending, nil
}

func (e *embedStore) SetEmbedding(_ context.Context, _ string, s *domain.Skill, vec []float32, _ string) error {
	if e.moved[s.ID] {
		return domain.ErrNotFound
	}
	e.stored[s.ID] = vec
	return nil
}

type stubEmbedClient struct {
	calls  int
	sent   [][]string
	vecs   [][]float32
	err    error
	short  bool
	model_ string
}

func (c *stubEmbedClient) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	c.calls++
	c.sent = append(c.sent, inputs)
	if c.err != nil {
		return nil, c.err
	}
	if c.short {
		return [][]float32{{0.1}}, nil
	}
	if c.vecs != nil {
		return c.vecs, nil
	}
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = []float32{float32(i)}
	}
	return out, nil
}

func (c *stubEmbedClient) Model() string {
	if c.model_ == "" {
		return "text-embedding-3-small"
	}
	return c.model_
}

func clientFor(c Client) ClientFor {
	return func(context.Context, string) (Client, error) { return c, nil }
}

func embedFixture(id, name string) *domain.Skill {
	return &domain.Skill{
		ID: id, CompanyID: "co-1", Name: name,
		WhenToUse: "The user asks about " + name + ".",
		Body:      "Do the thing.", Enabled: true, Source: domain.SkillSourceTenant,
	}
}

// What is embedded is the index line, because the index line is the only thing
// the model reads before deciding whether to open a procedure.
func TestWhatIsEmbeddedIsTheIndexLineAndNotTheBody(t *testing.T) {
	s := embedFixture("s1", "Weekly report")
	s.Body = "A body nobody should be ranking on."
	client := &stubEmbedClient{}
	NewEmbedder(newEmbedStore(), clientFor(client)).EmbedOne(context.Background(), "co-1", s)

	if len(client.sent) != 1 || len(client.sent[0]) != 1 {
		t.Fatalf("sent %v, want one input", client.sent)
	}
	if got := client.sent[0][0]; got != s.EmbedText() {
		t.Errorf("embedded %q, want the index line %q", got, s.EmbedText())
	}
	if got := client.sent[0][0]; got == s.IndexLine() {
		t.Error("the bullet went into the vector; EmbedText is IndexLine without it")
	}
}

// **The save is the authorship event and an embedding call is not part of it.**
// A provider outage must cost a tenant a ranking, not a procedure.
func TestAFailedEmbedIsSilentAndStoresNothing(t *testing.T) {
	store := newEmbedStore()
	client := &stubEmbedClient{err: errors.New("provider down")}
	NewEmbedder(store, clientFor(client)).EmbedOne(context.Background(), "co-1", embedFixture("s1", "Weekly report"))

	if len(store.stored) != 0 {
		t.Errorf("stored %v after a failed embedding call", store.stored)
	}
}

// A tenant with no embedding credentials gets (nil, nil) from the cache, which
// is the ordinary state rather than an error — and must not reach the provider.
func TestNoEmbeddingCredentialsIsNotAnError(t *testing.T) {
	store := newEmbedStore()
	e := NewEmbedder(store, func(context.Context, string) (Client, error) { return nil, nil })
	e.EmbedOne(context.Background(), "co-1", embedFixture("s1", "Weekly report"))

	n, err := e.Backfill(context.Background(), "co-1")
	if err != nil || n != 0 {
		t.Errorf("Backfill = (%d, %v), want (0, nil)", n, err)
	}
}

// A built-in has no row, so there is nothing to store a vector on and an id
// like `builtin:recurring-report` would reach a uuid column.
func TestABuiltinIsNeverEmbedded(t *testing.T) {
	store := newEmbedStore()
	client := &stubEmbedClient{}
	shipped := embedFixture("builtin:recurring-report", "Recurring report")
	shipped.Source = "builtin:recurring-report"
	NewEmbedder(store, clientFor(client)).EmbedOne(context.Background(), "co-1", shipped)

	if client.calls != 0 {
		t.Errorf("a shipped skill was sent to the embedding provider %d times", client.calls)
	}
}

// One batch call for the whole backlog, not one per skill: what is embedded is
// two hundred index lines at most.
func TestBackfillEmbedsThePendingSetInOneCall(t *testing.T) {
	store := newEmbedStore(embedFixture("s1", "Alpha"), embedFixture("s2", "Beta"), embedFixture("s3", "Gamma"))
	client := &stubEmbedClient{}
	n, err := NewEmbedder(store, clientFor(client)).Backfill(context.Background(), "co-1")

	if err != nil || n != 3 {
		t.Fatalf("Backfill = (%d, %v), want (3, nil)", n, err)
	}
	if client.calls != 1 {
		t.Errorf("the provider was called %d times for three skills", client.calls)
	}
}

// Position is the only thing pairing an input with its vector. A provider that
// returns a different count has broken that pairing, so nothing it sent back is
// safe to store — refused rather than truncated, which is Validate's rule.
func TestBackfillStoresNothingWhenTheProviderReturnsTheWrongCount(t *testing.T) {
	store := newEmbedStore(embedFixture("s1", "Alpha"), embedFixture("s2", "Beta"))
	client := &stubEmbedClient{short: true}
	n, err := NewEmbedder(store, clientFor(client)).Backfill(context.Background(), "co-1")

	if err != nil {
		t.Fatalf("Backfill errored: %v", err)
	}
	if n != 0 || len(store.stored) != 0 {
		t.Errorf("stored %d vectors from a mismatched batch: %v", n, store.stored)
	}
}

// An admin who edits the trigger sentence while the backfill is in flight must
// not be handed a vector for the sentence they just replaced.
func TestBackfillSkipsARowWhoseTextMovedUnderIt(t *testing.T) {
	store := newEmbedStore(embedFixture("s1", "Alpha"), embedFixture("s2", "Beta"))
	store.moved["s1"] = true
	client := &stubEmbedClient{}
	n, err := NewEmbedder(store, clientFor(client)).Backfill(context.Background(), "co-1")

	if err != nil {
		t.Fatalf("Backfill errored: %v", err)
	}
	if n != 1 {
		t.Errorf("stored %d, want only the row that had not moved", n)
	}
	if _, ok := store.stored["s1"]; ok {
		t.Error("a stale vector was stored for the edited row")
	}
}

// The trigger is a turn, so an unfixable tenant must not cost one failing API
// call per question asked.
func TestTheBackfillCooldownAllowsOneAttemptPerWindow(t *testing.T) {
	e := NewEmbedder(newEmbedStore(), clientFor(&stubEmbedClient{}))
	if !e.claim("co-1") {
		t.Fatal("the first attempt was refused")
	}
	if e.claim("co-1") {
		t.Error("a second attempt inside the cooldown was allowed")
	}
	if !e.claim("co-2") {
		t.Error("one company's cooldown blocked another's")
	}
}

// A deployment with no embedding wiring calls these unconditionally.
func TestANilEmbedderIsANoOp(t *testing.T) {
	var e *Embedder
	e.EmbedOne(context.Background(), "co-1", embedFixture("s1", "Alpha"))
	e.BackfillSoon(context.Background(), "co-1")
	if n, err := e.Backfill(context.Background(), "co-1"); n != 0 || err != nil {
		t.Errorf("Backfill on a nil embedder = (%d, %v)", n, err)
	}
	if NewEmbedder(nil, clientFor(&stubEmbedClient{})) != nil {
		t.Error("an embedder was built with no repository")
	}
}
