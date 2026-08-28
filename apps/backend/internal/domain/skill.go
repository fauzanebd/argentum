package domain

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// A skill is a tenant-authored, named procedure with a stated trigger (T-K1).
//
// Four fields carry the design. `Name` and `WhenToUse` are the only parts that
// travel in the system prompt — one index line per skill, every turn (T-K3).
// `Body` does not travel until the model calls `load_skill` (T-K4), which is
// what makes thirty procedures cost thirty lines rather than thirty procedures.
//
// **A skill grants nothing.** It is not a tool, it cannot widen a scope, and a
// body saying "query the HR database" on an agent scoped away from it produces
// a refused `run_sql` and a confused turn — the same thing T-S2 established for
// the persona: scoping is enforced at the tool, and a prompt saying "only use
// the finance database" is a wish.
//
// **The body is trusted text**, and that is an argued exception to T-H8's rule
// that a tool result is data rather than instruction. The basis is authorship,
// not channel: this product already trusts the persona and the company profile
// unfenced because an authenticated member typed them into the dashboard, and a
// skill sits beside them. Nothing that arrived *inside content* — a PDF, a
// warehouse row, an MCP result — may reach this struct without a human saving
// it. `docs/plan/07-agentic-skills-roadmap.md` §4 is the argument; `T-K2` is
// where it becomes code.
type Skill struct {
	ID        string    `json:"id"`
	CompanyID string    `json:"company_id"`
	Name      string    `json:"name"`
	WhenToUse string    `json:"when_to_use"`
	Body      string    `json:"body"`
	Enabled   bool      `json:"enabled"`
	Source    string    `json:"source"`
	CreatedBy string    `json:"created_by,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Embedding is the vector of EmbedText, used only to decide which skills
	// survive the index bound (T-K5). Nil is the ordinary state: a skill
	// written before 072, a tenant with no embedding credentials, and a
	// built-in all have none, and each of those ranks after the rows that do.
	//
	// `json:"-"` because it is 1,536 floats of derived data that no client has
	// a use for, and serialising it would make every list response an order of
	// magnitude larger than the text it describes.
	Embedding []float32 `json:"-"`
	// EmbeddingModel is which model produced Embedding. Two vectors from
	// different models are not comparable, so a deployment that changes its
	// embedding model needs to be able to find what predates the change.
	EmbeddingModel string `json:"embedding_model,omitempty"`
}

// The four caps, and none of them is tidiness.
//
// `MaxSkillNameChars` and `MaxSkillWhenToUseChars` bound the part that rides
// **every** turn: they are concatenated into one index line, so together they
// are what makes the always-on cost of this feature a number somebody can state
// rather than a function of how much a tenant typed. At these two caps a line
// tops out at 263 characters and the index at `SkillIndexMaxLines` tops out at
// ≈5,260 — which is why `SKILL_INDEX_MAX_CHARS` exists too, in the unit that
// matters.
//
// `MaxSkillBodyChars` bounds the part that only travels when asked for, which
// is why it can afford to be twenty times larger.
//
// `MaxSkillsPerCompany` bounds the table. Without it the index truncation is
// the only thing standing between a tenant and an unbounded list, and a
// truncation is a worse place to discover a limit than a save.
const (
	MaxSkillNameChars      = 60
	MaxSkillWhenToUseChars = 200
	MaxSkillBodyChars      = 8000
	MaxSkillsPerCompany    = 200
)

// SkillSourceTenant marks a skill an admin typed. Anything else is
// `builtin:<key>` — a skill shipped in `config/skills/` (T-K8), trusted on the
// same basis as `config/agent_templates.yaml`: it arrived in a commit somebody
// reviewed.
const SkillSourceTenant = "tenant"

// SkillSourceBuiltinPrefix is what a shipped skill's Source starts with, so
// "which of these did we write" is a prefix test rather than a second column.
const SkillSourceBuiltinPrefix = "builtin:"

// IsBuiltin reports whether this skill shipped with the product.
func (s *Skill) IsBuiltin() bool {
	return strings.HasPrefix(s.Source, SkillSourceBuiltinPrefix)
}

// IndexLine is the one line this skill contributes to every turn's system
// prompt. Defined here rather than in the composer because it is the thing the
// two caps above bound, and a caller that assembles it differently would make
// those caps describe a string nobody produces.
func (s *Skill) IndexLine() string {
	return "- " + s.EmbedText()
}

// EmbedText is what T-K5 embeds, and it is IndexLine without its bullet on
// purpose.
//
// **The ranker has to rank the text the model is shown.** What the model reads
// before deciding whether to open a procedure is the index line and nothing
// else — not the body, which does not travel until `load_skill` asks for it.
// Embedding the body instead would rank on prose that plays no part in the
// decision being ranked, which is a ranker answering a question nobody asked.
//
// Defined beside IndexLine and used by it, so the two cannot drift into
// describing different strings.
func (s *Skill) EmbedText() string {
	return s.Name + " — " + s.WhenToUse
}

// Validate refuses a skill that breaks a cap, **naming the field and the
// limit**, and never truncates.
//
// Truncation is the failure this method exists to prevent: a silently shortened
// procedure is a procedure whose last step vanished, and the last step is where
// "and do not include cancelled orders" lives. The tenant finds out at save
// time or not at all.
//
// Counted in runes rather than bytes. The caps exist to bound a prompt, and a
// prompt is charged in tokens; a rule that let an English procedure be 8,000
// characters and an Indonesian one 5,300 would be a cap on the alphabet.
func (s *Skill) Validate() error {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return wrapInvalid("a skill needs a name — it is how the agent refers to it")
	}
	if n := utf8.RuneCountInString(name); n > MaxSkillNameChars {
		return wrapInvalid("name is %d characters; the limit is %d, because it rides in every turn's prompt", n, MaxSkillNameChars)
	}
	when := strings.TrimSpace(s.WhenToUse)
	if when == "" {
		return wrapInvalid("a skill needs when_to_use — it is the only thing the agent reads before deciding to open it")
	}
	if n := utf8.RuneCountInString(when); n > MaxSkillWhenToUseChars {
		return wrapInvalid("when_to_use is %d characters; the limit is %d, because it rides in every turn's prompt", n, MaxSkillWhenToUseChars)
	}
	body := strings.TrimSpace(s.Body)
	if body == "" {
		return wrapInvalid("a skill needs a body — an index line with no procedure behind it is a promise the agent cannot keep")
	}
	if n := utf8.RuneCountInString(body); n > MaxSkillBodyChars {
		return wrapInvalid("body is %d characters; the limit is %d", n, MaxSkillBodyChars)
	}
	if s.Source == "" {
		s.Source = SkillSourceTenant
	}
	if s.Source != SkillSourceTenant && !s.IsBuiltin() {
		return wrapInvalid("source %q is neither %q nor %s<key>", s.Source, SkillSourceTenant, SkillSourceBuiltinPrefix)
	}
	s.Name, s.WhenToUse, s.Body = name, when, body
	return nil
}

// ErrSkillLimit is the refusal for the fourth cap. Its own error rather than a
// wrapInvalid string because the handler answers it with 409 rather than 400:
// the request is well-formed and the workspace is full, which is a different
// thing for a caller to act on.
var ErrSkillLimit = fmt.Errorf("%w: this workspace already has %d skills, which is the limit; disable or delete one before adding another",
	ErrInvalidInput, MaxSkillsPerCompany)

// AllowsSkill reports whether this agent may be offered the given skill.
//
// **Empty means every enabled company skill**, which is AllowsSource's rule and
// the opposite of AllowsMCPServer's. The asymmetry is deliberate and it is the
// consequence that decides it: an MCP binding hands an agent a capability in a
// third-party system we hold a token for, while a skill hands it nothing — an
// irrelevant one in an index is a wasted line the model will not open, and
// empty-means-none would make every skill written after an agent was created
// invisible to it.
func (a *Agent) AllowsSkill(skillID string) bool {
	return len(a.SkillIDs) == 0 || slices.Contains(a.SkillIDs, skillID)
}

// SkillRepository is the persistence contract.
//
// Every method takes companyID beside the row id, for MCPServerRepo's reason:
// these are bare uuids in an admin URL, and a repository that will answer for
// any company is one forgotten check away from a cross-tenant read of the
// tenant's own written procedures.
type SkillRepository interface {
	Create(ctx context.Context, s *Skill) error
	// GetByID answers ErrNotFound for another company's id. Not
	// ErrUnauthorized: a 404 is not a directory, which is the same rule
	// `load_skill`'s refusals follow.
	GetByID(ctx context.Context, companyID, id string) (*Skill, error)
	// GetByName resolves what the model read off the index. Case-insensitive,
	// because the index is prose and a model retyping "Weekly Sales Report" as
	// "weekly sales report" has named the same procedure.
	GetByName(ctx context.Context, companyID, name string) (*Skill, error)
	ListByCompany(ctx context.Context, companyID string) ([]*Skill, error)
	// ListEnabledForIndex returns the enabled skills in the order T-K3
	// truncates them, so a tenant who crosses a bound loses the same skills
	// every turn rather than a different one each time.
	ListEnabledForIndex(ctx context.Context, companyID string) ([]*Skill, error)
	// ListEnabledRankedForIndex returns the same set ordered by how close each
	// skill's EmbedText is to this turn's question (T-K5).
	//
	// **Only called when the alphabetical order would drop something**, which
	// is the property that keeps this from costing anything: below the bound
	// nothing is lost, and a set that reordered every turn would invalidate the
	// cached system-prompt prefix the index was deliberately put inside.
	//
	// Rows with no vector sort last and keep lower(name) among themselves, so a
	// company that has never been embedded gets exactly ListEnabledForIndex's
	// answer.
	ListEnabledRankedForIndex(ctx context.Context, companyID string, queryVec []float32) ([]*Skill, error)
	// ListUnembedded returns the enabled skills that have no vector, so the
	// backfill can find what a write-time embed missed.
	ListUnembedded(ctx context.Context, companyID string) ([]*Skill, error)
	// SetEmbedding stores one skill's vector. Separate from Update because it
	// writes derived data and must not touch updated_by or updated_at: a
	// backfill is not an edit, and a tenant looking at "last changed by" should
	// not see a vector job there.
	//
	// It takes the whole skill rather than an id because the write is
	// conditional on the text still being what was embedded — see the
	// implementation — and answers ErrNotFound when it no longer is.
	SetEmbedding(ctx context.Context, companyID string, s *Skill, vec []float32, model string) error
	Update(ctx context.Context, s *Skill) error
	Delete(ctx context.Context, companyID, id string) error
	CountByCompany(ctx context.Context, companyID string) (int, error)
	// SetAgentBinding replaces one agent's binding. An empty list means every
	// enabled company skill; see AllowsSkill.
	SetAgentBinding(ctx context.Context, companyID, agentID string, skillIDs []string) error
	// AgentBinding returns the skill ids bound to one agent, empty for an
	// agent that has never been bound.
	AgentBinding(ctx context.Context, companyID, agentID string) ([]string, error)
}
