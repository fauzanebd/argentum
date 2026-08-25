package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
)

// Built-in skills are code, not tenant rows (T-K8).
//
// The argument is `config/agent_templates.yaml`'s, unchanged: a guess that
// turns out wrong is a one-line commit here — reaching every tenant who has not
// written their own — rather than a migration that cannot reach the tenant who
// has. `config/guardrails.yaml` is the prior art, down to the golden test over
// the real files.
//
// **The rule that keeps this set small, and it is the one a reviewer should
// enforce: a built-in skill must not restate a guideline.** Anything the model
// should do on every turn belongs in the system prompt, where it is paid for
// once; a skill is for what it should do on *some* turns. A skill repeating an
// always-on rule pays for it twice — once in the floor, once in an index line
// and a `load_skill` round trip — and buys a precedence question in exchange,
// because the model now holds one instruction from two channels with no
// ordering between them.
//
// That rule is why the shipped set is two files rather than a folder that
// grows. A third candidate was drafted and cut for restating the no-fabrication
// guideline, which is already unconditional and already at 3/3 in the eval.

// LoadBuiltins reads every `*.md` in dir as a skill.
//
// **A malformed file fails the boot, not the request.** It is the rule
// `tools.AllNames` exists for: a shipped skill that breaks a cap is a mistake
// in this repository, and the deployment that discovers it should be every
// deployment at once rather than the one tenant whose turn happened to compose
// an index. An absent directory is not an error — a deployment may ship none.
func LoadBuiltins(dir string) ([]*domain.Skill, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read built-in skills from %s: %w", dir, err)
	}

	var out []*domain.Skill
	seen := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		key := strings.TrimSuffix(e.Name(), ".md")
		s, err := parseBuiltin(key, string(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		// Validated with the tenant rules, not a looser set. A shipped skill
		// that would be refused from the API is a shipped skill that lies about
		// what the caps mean.
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		// Names are what the model sends to `load_skill`, so two files claiming
		// one name is a tool call with no correct answer — the same reason 069
		// carries a unique index on (company_id, lower(name)).
		lower := strings.ToLower(s.Name)
		if prev, dup := seen[lower]; dup {
			return nil, fmt.Errorf("%s: name %q is already used by %s", path, s.Name, prev)
		}
		seen[lower] = path
		out = append(out, s)
	}

	// Sorted by name, because the index is truncated deterministically and a
	// directory listing's order is the filesystem's business.
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// parseBuiltin reads `---` front matter followed by the markdown body.
//
// Hand-parsed rather than run through a YAML library, and the reason is the
// body: everything after the closing marker is markdown that must survive
// exactly as written, including the `---` a horizontal rule would produce. A
// YAML document loader would want to own the whole file.
func parseBuiltin(key, raw string) (*domain.Skill, error) {
	raw = strings.TrimLeft(raw, "\ufeff \t\r\n")
	if !strings.HasPrefix(raw, "---") {
		return nil, fmt.Errorf("no front matter: a built-in skill starts with a --- block naming name and when_to_use")
	}
	rest := strings.TrimPrefix(raw, "---")
	rest = strings.TrimLeft(rest, "\r\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, fmt.Errorf("front matter is not closed with ---")
	}
	head, body := rest[:end], rest[end+len("\n---"):]

	s := &domain.Skill{Source: domain.SkillSourceBuiltinPrefix + key, Enabled: true}
	var field *string
	for _, line := range strings.Split(head, "\n") {
		trimmed := strings.TrimRight(line, "\r")
		// A continuation line: front matter values wrap, because when_to_use is
		// prose and 200 characters is longer than anyone wants on one line.
		if field != nil && (strings.HasPrefix(trimmed, "  ") || strings.HasPrefix(trimmed, "\t")) {
			*field = strings.TrimSpace(*field + " " + strings.TrimSpace(trimmed))
			continue
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			if strings.TrimSpace(trimmed) == "" {
				continue
			}
			return nil, fmt.Errorf("front-matter line %q is not `key: value`", trimmed)
		}
		switch strings.TrimSpace(name) {
		case "name":
			s.Name = strings.TrimSpace(value)
			field = &s.Name
		case "when_to_use":
			s.WhenToUse = strings.TrimSpace(value)
			field = &s.WhenToUse
		default:
			return nil, fmt.Errorf("unknown front-matter key %q; a built-in skill carries name and when_to_use", strings.TrimSpace(name))
		}
	}
	s.Body = strings.TrimSpace(body)
	return s, nil
}

// WithBuiltins wraps a skill repository so the shipped procedures appear
// alongside the tenant's own.
//
// A decorator rather than a merge at each call site, because there are two call
// sites — the index the prompt is composed from and `load_skill`'s lookup — and
// a shipped skill visible in one but not the other is the worst available
// outcome: an index line the agent can read and cannot open, which looks like a
// bug in the model.
//
// **Tenant skills come first and built-ins after them**, so when an index is
// truncated the tenant loses ours before they lose theirs. Their procedures are
// about their business; ours are about method.
//
// **A tenant may shadow a built-in by name.** `GetByName` asks the repository
// first, so a company skill called "Period-over-period comparison" wins and the
// shipped one becomes unreachable for that tenant. That is the right way round:
// the tenant who wrote a procedure with that name meant theirs.
func WithBuiltins(repo domain.SkillRepository, builtins []*domain.Skill) domain.SkillRepository {
	if repo == nil || len(builtins) == 0 {
		return repo
	}
	return &withBuiltins{SkillRepository: repo, builtins: builtins}
}

type withBuiltins struct {
	domain.SkillRepository
	builtins []*domain.Skill
}

func (w *withBuiltins) ListEnabledForIndex(ctx context.Context, companyID string) ([]*domain.Skill, error) {
	rows, err := w.SkillRepository.ListEnabledForIndex(ctx, companyID)
	if err != nil {
		return nil, err
	}
	taken := make(map[string]bool, len(rows))
	for _, r := range rows {
		taken[strings.ToLower(r.Name)] = true
	}
	out := rows
	for _, b := range w.builtins {
		if taken[strings.ToLower(b.Name)] {
			continue
		}
		out = append(out, w.forCompany(b, companyID))
	}
	return out, nil
}

func (w *withBuiltins) GetByName(ctx context.Context, companyID, name string) (*domain.Skill, error) {
	row, err := w.SkillRepository.GetByName(ctx, companyID, name)
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	for _, b := range w.builtins {
		if strings.EqualFold(b.Name, name) {
			return w.forCompany(b, companyID), nil
		}
	}
	return nil, domain.ErrNotFound
}

// forCompany returns a copy carrying the asking tenant's id.
//
// A copy because the loaded set is shared by every request in the process, and
// handing a caller the shared pointer is how one tenant's company id ends up on
// another's turn.
//
// **The id is the source string**, which is stable across restarts and cannot
// collide with a uuid. It matters because both the index filter and
// `load_skill`'s binding check compare ids: an agent with an explicit
// `agent_skills` binding names uuids, none of which is this, so a narrowed
// agent is not offered the shipped set either. That is the same
// empty-means-everything rule the tenant's own skills follow, and an admin who
// narrowed an agent deliberately gets the narrowing they asked for.
func (w *withBuiltins) forCompany(b *domain.Skill, companyID string) *domain.Skill {
	c := *b
	c.ID = b.Source
	c.CompanyID = companyID
	return &c
}
