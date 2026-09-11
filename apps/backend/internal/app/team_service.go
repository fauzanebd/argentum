package app

import (
	"github.com/sirupsen/logrus"

	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/domain"
)

// InviteTTL is how long an invite link stays usable. Seven days is long enough
// to survive a weekend and a forwarded email, short enough that a token
// sitting in an inbox archive is not a permanent way in.
const InviteTTL = 7 * 24 * time.Hour

// TeamService owns the membership lifecycle of one company: who is in it, what
// role they hold, and how they got there. Every method is company-scoped —
// callers pass the company from the JWT, never from the request body.
type TeamService struct {
	users   domain.UserRepository
	invites domain.UserInviteRepository
	now     func() time.Time
	// mail delivers the invitation (T-F6). Optional, and absent is the state
	// this product shipped in for two months: the route still returns the
	// token, and the dashboard still renders a link for the admin to copy.
	mail InviteMailer
}

// InviteMailer is the slice of the email surface this service needs. Declared
// here rather than taking internal/email so a test can answer it without a
// relay, and so the only thing this package knows about email is that it can
// fail and that it can be absent.
type InviteMailer interface {
	SendInvite(ctx context.Context, to string, d InviteMail) error
	Enabled() bool
}

// InviteMail is what the invitation says, assembled by whoever knows the
// company's name and the deployment's base URL — not by this service, which
// knows neither.
type InviteMail struct {
	CompanyID string
	InvitedBy string
	Token     string
	ExpiresIn time.Duration
}

// NewTeamService wires the repositories. now is injectable so tests can drive
// expiry without sleeping.
func NewTeamService(users domain.UserRepository, invites domain.UserInviteRepository) *TeamService {
	return &TeamService{users: users, invites: invites, now: time.Now}
}

// WithMailer makes the invitation arrive (T-F6). Optional in the builder, like
// every other outbound channel in this codebase: a deployment with no relay
// keeps the behaviour it had, where the link comes back in the response and a
// human forwards it.
func (s *TeamService) WithMailer(m InviteMailer) *TeamService {
	s.mail = m
	return s
}

// Member is one row of the team list: a user plus the invite state that
// explains why they cannot log in yet, if they cannot.
type Member struct {
	ID              string      `json:"id"`
	Email           string      `json:"email"`
	Role            domain.Role `json:"role"`
	Status          string      `json:"status"` // active | pending | deactivated
	CreatedAt       time.Time   `json:"created_at"`
	InviteSentAt    *time.Time  `json:"invite_sent_at,omitempty"`
	InviteExpiresAt *time.Time  `json:"invite_expires_at,omitempty"`
}

// InviteResult carries the one and only copy of the plaintext token.
type InviteResult struct {
	Member    Member    `json:"member"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	// Emailed says whether the invitation actually went out (T-F6). False
	// covers three different situations — no relay configured, the send failed,
	// no base URL to build a link with — and the dashboard treats all three the
	// same way, by showing the link to copy. What it must not do is say "sent"
	// when nothing was.
	Emailed bool `json:"emailed"`
}

// List returns every account in the company, pending ones included, with the
// invite metadata joined in so the dashboard can show "invited 3 days ago,
// expires in 4" without a second round trip.
func (s *TeamService) List(ctx context.Context, companyID string) ([]Member, error) {
	users, err := s.users.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	open, err := s.invites.ListOpenByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	byEmail := make(map[string]*domain.UserInvite, len(open))
	for _, inv := range open {
		byEmail[strings.ToLower(inv.Email)] = inv
	}

	out := make([]Member, 0, len(users))
	for _, u := range users {
		m := Member{
			ID:        u.ID,
			Email:     u.Email,
			Role:      u.Role,
			Status:    memberStatus(u),
			CreatedAt: u.CreatedAt,
		}
		if inv := byEmail[strings.ToLower(u.Email)]; inv != nil {
			sent, exp := inv.CreatedAt, inv.ExpiresAt
			m.InviteSentAt, m.InviteExpiresAt = &sent, &exp
		}
		out = append(out, m)
	}
	return out, nil
}

func memberStatus(u *domain.User) string {
	switch {
	case u.DeactivatedAt != nil:
		return "deactivated"
	case u.ActivatedAt == nil:
		return "pending"
	default:
		return "active"
	}
}

// Invite creates a pending user and a single-use token for them. Re-inviting
// an address that is still pending replaces its token rather than erroring:
// "the email never arrived" is the common case, and the alternative is an
// admin who has to revoke before they can re-send.
func (s *TeamService) Invite(ctx context.Context, companyID, invitedBy, email string, role domain.Role) (*InviteResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, fmt.Errorf("%w: a valid email is required", domain.ErrInvalidInput)
	}
	if !role.Valid() {
		return nil, fmt.Errorf("%w: role must be admin or member", domain.ErrInvalidInput)
	}

	existing, err := s.users.GetByEmail(ctx, email)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	var user *domain.User
	switch {
	case existing == nil:
		user = &domain.User{CompanyID: companyID, Email: email, Role: role}
		if err := s.users.CreatePending(ctx, user); err != nil {
			return nil, err
		}
	case existing.CompanyID != companyID || !existing.Pending():
		// The address belongs to an active account, or to a pending one in
		// another company. Either way this admin cannot claim it, and the
		// message is deliberately the same for both so the endpoint is not a
		// cross-tenant "does this person have an account?" oracle.
		return nil, domain.ErrAlreadyExists
	default:
		// Still pending in this company: re-issue against the same row, and
		// let the new invite's role win in case the admin changed their mind.
		user = existing
		if user.Role != role {
			if err := s.users.UpdateRole(ctx, companyID, user.ID, role); err != nil {
				return nil, err
			}
			user.Role = role
		}
		if err := s.invites.DeleteOpenFor(ctx, companyID, email); err != nil {
			return nil, err
		}
	}

	token, hash, err := auth.NewInviteToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	inv := &domain.UserInvite{
		CompanyID: companyID,
		Email:     email,
		Role:      role,
		TokenHash: hash,
		ExpiresAt: now.Add(InviteTTL),
		InvitedBy: invitedBy,
	}
	if err := s.invites.Create(ctx, inv); err != nil {
		return nil, err
	}

	// The mail goes out *after* the invite is durable, and a failure to send is
	// logged rather than returned (T-F6).
	//
	// **The token is in the response either way**, and that is the whole design
	// of this step. An invite that could not be mailed is still a usable invite
	// — the admin copies the link, which is what every invite in this product
	// did before today — whereas returning an error would roll a perfectly good
	// invitation back because a relay was briefly unreachable. The one thing
	// that must not happen is silence, so the failure is a Warn line naming the
	// company and not the address.
	emailed := false
	if s.mail != nil && s.mail.Enabled() {
		err := s.mail.SendInvite(ctx, email, InviteMail{
			CompanyID: companyID,
			InvitedBy: invitedBy,
			Token:     token,
			ExpiresIn: InviteTTL,
		})
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": companyID,
			}).Warn("invite created but could not be emailed; the link is in the response")
		} else {
			emailed = true
		}
	}

	return &InviteResult{
		Member: Member{
			ID:              user.ID,
			Email:           user.Email,
			Role:            role,
			Status:          "pending",
			CreatedAt:       user.CreatedAt,
			InviteSentAt:    &inv.CreatedAt,
			InviteExpiresAt: &inv.ExpiresAt,
		},
		Token:     token,
		ExpiresAt: inv.ExpiresAt,
		Emailed:   emailed,
	}, nil
}

// InvitePreview is what the accept page shows before asking for a password.
type InvitePreview struct {
	Email     string      `json:"email"`
	Role      domain.Role `json:"role"`
	CompanyID string      `json:"company_id"`
	ExpiresAt time.Time   `json:"expires_at"`
}

// Preview resolves a token without consuming it, so the accept page can say
// "this link expired" up front instead of after the invitee has typed a
// password twice.
func (s *TeamService) Preview(ctx context.Context, token string) (*InvitePreview, error) {
	inv, err := s.openInvite(ctx, token)
	if err != nil {
		return nil, err
	}
	return &InvitePreview{
		Email:     inv.Email,
		Role:      inv.Role,
		CompanyID: inv.CompanyID,
		ExpiresAt: inv.ExpiresAt,
	}, nil
}

// Accept activates the pending user behind a token and returns them ready for
// login. It does not mint tokens itself — the handler hands the user to
// AuthService.IssueSession, so one place decides what a session looks like.
func (s *TeamService) Accept(ctx context.Context, token, password string) (*domain.User, error) {
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	inv, err := s.openInvite(ctx, token)
	if err != nil {
		return nil, err
	}
	user, err := s.users.GetByEmail(ctx, inv.Email)
	if err != nil {
		return nil, err
	}
	if user.CompanyID != inv.CompanyID {
		// The pending row was deleted and the address re-registered elsewhere
		// between invite and accept. Refuse rather than activate a stranger.
		return nil, domain.ErrNotFound
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	now := s.now()
	// Activate first: it is the guarded write, so if two accepts race the
	// loser stops here and never marks the invite consumed on the winner's
	// behalf.
	if err := s.users.Activate(ctx, user.ID, hash, now); err != nil {
		return nil, err
	}
	if err := s.invites.MarkAccepted(ctx, inv.ID, now); err != nil {
		return nil, err
	}

	user.PasswordHash = hash
	user.ActivatedAt = &now
	user.Role = inv.Role
	return user, nil
}

// openInvite maps a plaintext token to an invite that is still usable. An
// unknown, consumed or expired token all yield ErrNotFound: the caller has no
// business distinguishing a typo from a used link.
func (s *TeamService) openInvite(ctx context.Context, token string) (*domain.UserInvite, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, domain.ErrNotFound
	}
	inv, err := s.invites.GetByTokenHash(ctx, auth.HashInviteToken(token))
	if err != nil {
		return nil, err
	}
	if !inv.Pending(s.now()) {
		return nil, domain.ErrNotFound
	}
	return inv, nil
}

// ChangeRole moves a member between admin and member. Demoting the last admin
// is refused for the same reason removing them is: the company would have
// nobody able to invite anyone back.
func (s *TeamService) ChangeRole(ctx context.Context, companyID, targetID string, role domain.Role) error {
	if !role.Valid() {
		return fmt.Errorf("%w: role must be admin or member", domain.ErrInvalidInput)
	}
	target, err := s.mustBelong(ctx, companyID, targetID)
	if err != nil {
		return err
	}
	if target.Role == role {
		return nil
	}
	if target.Role == domain.RoleAdmin {
		if err := s.guardLastAdmin(ctx, companyID, target); err != nil {
			return err
		}
	}
	return s.users.UpdateRole(ctx, companyID, targetID, role)
}

// Remove deactivates an active member, or deletes a pending one outright. The
// difference matters: a pending row holds a globally-unique email hostage, so
// revoking an invite has to give the address back.
func (s *TeamService) Remove(ctx context.Context, companyID, targetID string) error {
	target, err := s.mustBelong(ctx, companyID, targetID)
	if err != nil {
		return err
	}
	if target.Role == domain.RoleAdmin {
		if err := s.guardLastAdmin(ctx, companyID, target); err != nil {
			return err
		}
	}
	if target.Pending() {
		if err := s.invites.DeleteOpenFor(ctx, companyID, target.Email); err != nil {
			return err
		}
		return s.users.Delete(ctx, companyID, targetID)
	}
	return s.users.Deactivate(ctx, companyID, targetID, s.now())
}

// guardLastAdmin refuses the change when target is the only admin who can
// currently act. A pending or already-deactivated admin does not count towards
// the total, so demoting the sole *active* admin is blocked even when a second
// admin exists on paper but has never accepted their invite.
func (s *TeamService) guardLastAdmin(ctx context.Context, companyID string, target *domain.User) error {
	if !target.Active() {
		return nil
	}
	n, err := s.users.CountActiveAdmins(ctx, companyID)
	if err != nil {
		return err
	}
	if n <= 1 {
		return domain.ErrLastAdmin
	}
	return nil
}

// mustBelong loads a user and refuses to admit that users in other companies
// exist: a wrong-tenant id is a 404, not a 403.
func (s *TeamService) mustBelong(ctx context.Context, companyID, userID string) (*domain.User, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	return u, nil
}
