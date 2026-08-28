package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/llmtenant"
	"github.com/fauzanebd/argentum/internal/skill"
)

// NewSkillEmbedder builds T-K5's embedder over the per-tenant embedding cache.
//
// **It exists in this package because of an import cycle**, which is worth
// stating so nobody moves it somewhere tidier. `internal/config` imports
// `internal/skill` for T-K3's default bounds, and `internal/llmtenant` imports
// `internal/config`; so the skill package cannot name the cache, and the two
// wirings that hold one — `cmd/api` and `internal/bootstrap` — would otherwise
// each carry their own copy of this adapter.
//
// **The nil check is the substance of it.** The cache answers (nil, nil) for a
// tenant with no embedding credentials, which every existing caller branches
// on; returning that nil directly through a differently-typed interface would
// produce a non-nil skill.Client wrapping a nil value, and the branch would
// stop working everywhere at once.
func NewSkillEmbedder(repo domain.SkillRepository, cache *llmtenant.EmbeddingCache) *skill.Embedder {
	if cache == nil {
		return nil
	}
	return skill.NewEmbedder(repo, func(ctx context.Context, companyID string) (skill.Client, error) {
		client, err := cache.For(ctx, companyID)
		if err != nil || client == nil {
			return nil, err
		}
		return client, nil
	})
}

// SkillService is the tenant's written procedures: create, edit, enable,
// delete, and bind to an agent (T-K1).
//
// Nothing in this file reaches a turn. The index that rides the prompt is
// `T-K3` and the tool that opens a body is `T-K4`, split for the reason T-M1
// was split from T-M2: a CRUD surface and a caps policy do not belong in the
// ticket that rewires prompt composition.
//
// **The save is the authorship event**, and that sentence is the whole trust
// argument in one line. A body reaches the model unfenced because an
// authenticated member of the company typed it — the same basis the persona
// stands on. Every path that writes here goes through an admin-authenticated
// request; `T-K7`'s harvested draft is a suggestion in a form, and it becomes
// trusted at the moment a human presses save rather than at the moment the
// agent proposed it.
type SkillService struct {
	skills   domain.SkillRepository
	agents   domain.AgentRepository
	embedder *skill.Embedder
	// index is the repository a *turn* reads, which is the tenant's rows with
	// the shipped set merged in (T-K8). Separate from `skills` because the CRUD
	// surface is deliberately the tenant's own rows only — an admin edits what
	// they wrote — while the answer to "what does my index cost every turn"
	// has to count what the prompt actually carries. Nil falls back to
	// `skills`, which is a smaller number and an honest one.
	index domain.SkillRepository
}

// NewSkillService wires the service.
func NewSkillService(skills domain.SkillRepository, agents domain.AgentRepository) *SkillService {
	return &SkillService{skills: skills, agents: agents}
}

// WithEmbedder enables T-K5's write-time vector. Nil leaves it off, which is a
// supported state: a deployment with no embedding credentials keeps T-K3's
// alphabetical index and loses nothing until a tenant crosses the bound.
func (s *SkillService) WithEmbedder(e *skill.Embedder) *SkillService {
	s.embedder = e
	return s
}

// WithIndexReader supplies the repository a turn composes its index from, so
// the preview surface can show the tenant the block their prompt really
// carries rather than a subset of it (T-K6).
func (s *SkillService) WithIndexReader(repo domain.SkillRepository) *SkillService {
	s.index = repo
	return s
}

// Preview renders a draft without saving it.
//
// Nothing here writes, and nothing here is an authorship event: what comes back
// is a rendering of text the caller already holds. It deliberately does not
// refuse an over-cap draft — an author who has pasted too much needs to see
// the counter and the sentence, not an error page instead of their own words.
func (s *SkillService) Preview(in *domain.Skill) *SkillPreview {
	draft := *in
	draft.Name = strings.TrimSpace(draft.Name)
	draft.WhenToUse = strings.TrimSpace(draft.WhenToUse)
	draft.Body = strings.TrimSpace(draft.Body)

	p := &SkillPreview{
		IndexLine:      draft.IndexLine(),
		FramedBody:     skill.Frame(draft.Name, draft.Body),
		NameChars:      utf8.RuneCountInString(draft.Name),
		WhenToUseChars: utf8.RuneCountInString(draft.WhenToUse),
		BodyChars:      utf8.RuneCountInString(draft.Body),
	}
	p.IndexLineChars = utf8.RuneCountInString(p.IndexLine)
	check := draft
	check.Source = domain.SkillSourceTenant
	if err := check.Validate(); err != nil {
		p.Refusal = err.Error()
	}
	return p
}

// IndexCost composes this company's index exactly as a turn would and reports
// what it cost.
//
// Composed rather than estimated. The bounds interact — whichever binds first
// binds — and a client adding up name lengths would be reimplementing the
// truncation rule in a place nobody would think to update.
func (s *SkillService) IndexCost(ctx context.Context, companyID string, maxLines, maxChars int) (*SkillIndexCost, error) {
	repo := s.index
	if repo == nil {
		repo = s.skills
	}
	rows, err := repo.ListEnabledForIndex(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if maxLines <= 0 {
		maxLines = skill.DefaultIndexMaxLines
	}
	if maxChars <= 0 {
		maxChars = skill.DefaultIndexMaxChars
	}
	// A nil `allowed` is the unrestricted view — what an agent with no binding
	// is offered, which is every enabled skill. That is the right number for a
	// settings screen: it is the workspace's cost, not one agent's.
	block, dropped := skill.Compose(rows, nil, maxLines, maxChars)
	cost := &SkillIndexCost{MaxChars: maxChars, MaxLines: maxLines, Dropped: dropped}
	if cost.Dropped == nil {
		cost.Dropped = []string{}
	}
	if block != "" {
		cost.Chars = utf8.RuneCountInString(block)
		cost.Lines = skill.Lines(block)
	}
	return cost, nil
}

// Create validates the caps, checks the per-company limit, and writes.
//
// The count is checked before the insert rather than enforced by a constraint,
// because the answer a tenant needs is a sentence naming the limit and what to
// do about it. A race between two admins can put a company one row over; that
// is a better outcome than a Postgres error string reaching a form.
func (s *SkillService) Create(ctx context.Context, companyID, userID string, skill *domain.Skill) (*domain.Skill, error) {
	skill.CompanyID = companyID
	skill.CreatedBy = userID
	skill.UpdatedBy = userID
	if err := skill.Validate(); err != nil {
		return nil, err
	}
	n, err := s.skills.CountByCompany(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("count skills: %w", err)
	}
	if n >= domain.MaxSkillsPerCompany {
		return nil, domain.ErrSkillLimit
	}
	if err := s.skills.Create(ctx, skill); err != nil {
		return nil, err
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"skill_id":   skill.ID,
		"name":       skill.Name,
		"created_by": userID,
	}).Info("skill: created; its index line rides every turn from now on")
	// Synchronous, and inside the save the admin is already waiting on. It is
	// one short embedding call, it makes the skill rankable the moment it
	// exists rather than at the next backfill, and it cannot fail the save —
	// EmbedOne returns nothing to fail with (T-K5).
	s.embedder.EmbedOne(ctx, companyID, skill)
	return skill, nil
}

// Update edits an existing skill. The load-then-write is what makes another
// company's id a 404 rather than a silent no-op, and what keeps `source` and
// `created_by` out of the caller's reach: a tenant must not be able to relabel
// their own text as `builtin:` and inherit the argument that a commit somebody
// reviewed is what makes a shipped skill trustworthy.
func (s *SkillService) Update(ctx context.Context, companyID, userID, id string, in *domain.Skill) (*domain.Skill, error) {
	existing, err := s.skills.GetByID(ctx, companyID, id)
	if err != nil {
		return nil, err
	}
	// Captured before the assignment because the repository clears the vector
	// when either of these moves, and re-embedding a skill whose trigger
	// sentence nobody touched would be paying for a vector we already hold.
	wasIndexedAs := existing.EmbedText()
	existing.Name = in.Name
	existing.WhenToUse = in.WhenToUse
	existing.Body = in.Body
	existing.Enabled = in.Enabled
	existing.UpdatedBy = userID
	if err := existing.Validate(); err != nil {
		return nil, err
	}
	if err := s.skills.Update(ctx, existing); err != nil {
		return nil, err
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"skill_id":   existing.ID,
		"enabled":    existing.Enabled,
		"updated_by": userID,
	}).Info("skill: updated")
	if existing.EmbedText() != wasIndexedAs {
		s.embedder.EmbedOne(ctx, companyID, existing)
	}
	return existing, nil
}

// Get returns one skill, 404 for another company's.
func (s *SkillService) Get(ctx context.Context, companyID, id string) (*domain.Skill, error) {
	return s.skills.GetByID(ctx, companyID, id)
}

// List returns every skill the company has, enabled or not. The dashboard needs
// the disabled ones — off is a first-class state and a procedure being revised
// has to be findable.
func (s *SkillService) List(ctx context.Context, companyID string) ([]*domain.Skill, error) {
	return s.skills.ListByCompany(ctx, companyID)
}

// Delete removes a skill and, through 069's CASCADE, every agent binding to it.
func (s *SkillService) Delete(ctx context.Context, companyID, id string) error {
	if err := s.skills.Delete(ctx, companyID, id); err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{"company_id": companyID, "skill_id": id}).
		Info("skill: deleted; agent bindings to it went with it")
	return nil
}

// SetAgentBinding replaces which skills one agent is offered.
//
// **An empty list means every enabled company skill**, not none — AllowsSkill
// is where that is written down, and this method logs which of the two states
// it just wrote so a support question about a missing skill has an answer in
// the log rather than a guess.
//
// Every id is checked against the company before the write. The repository
// checks it again in SQL; this layer exists to name the offending id, because
// "one of these ids is not yours" is a form error somebody has to fix.
func (s *SkillService) SetAgentBinding(ctx context.Context, companyID, agentID string, skillIDs []string) error {
	if _, err := s.agents.GetByID(ctx, companyID, agentID); err != nil {
		return err
	}
	for _, id := range skillIDs {
		if _, err := s.skills.GetByID(ctx, companyID, id); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return fmt.Errorf("%w: skill %q does not belong to this workspace", domain.ErrInvalidInput, id)
			}
			return err
		}
	}
	if err := s.skills.SetAgentBinding(ctx, companyID, agentID, skillIDs); err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"agent_id":   agentID,
		"bound":      len(skillIDs),
		"meaning":    bindingMeaning(len(skillIDs)),
	}).Info("skill: agent binding replaced")
	return nil
}

// AgentBinding returns one agent's bound ids.
func (s *SkillService) AgentBinding(ctx context.Context, companyID, agentID string) ([]string, error) {
	if _, err := s.agents.GetByID(ctx, companyID, agentID); err != nil {
		return nil, err
	}
	return s.skills.AgentBinding(ctx, companyID, agentID)
}

// bindingMeaning spells out the empty case in the log line, because this is the
// one place in the tree where two adjacent bindings — skills and MCP servers —
// read the empty list in opposite directions.
func bindingMeaning(n int) string {
	if n == 0 {
		return "every enabled company skill"
	}
	return "only the listed skills"
}
