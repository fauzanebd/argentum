package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

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
	skills domain.SkillRepository
	agents domain.AgentRepository
}

// NewSkillService wires the service.
func NewSkillService(skills domain.SkillRepository, agents domain.AgentRepository) *SkillService {
	return &SkillService{skills: skills, agents: agents}
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
