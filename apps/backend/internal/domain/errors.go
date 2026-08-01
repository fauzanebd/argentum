package domain

import "errors"

// Sentinel domain errors. Adapters wrap their underlying driver errors with
// these so application code can switch on stable values.
var (
	ErrNotFound       = errors.New("not found")
	ErrAlreadyExists  = errors.New("already exists")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrInvalidInput   = errors.New("invalid input")
	ErrUnsupportedDB  = errors.New("unsupported database type")
	ErrConflict       = errors.New("conflict")
	ErrCredentialsBad = errors.New("invalid credentials")

	// ErrAccountInactive covers both halves of the invite lifecycle: an
	// account that has not accepted its invite yet, and one an admin has
	// deactivated. It is only ever returned *after* the password check passes,
	// so distinguishing it from ErrCredentialsBad leaks nothing to someone who
	// does not already hold the password.
	ErrAccountInactive = errors.New("account is not active")

	// ErrLastAdmin guards the one state a company cannot recover from through
	// the UI: nobody left who can invite or promote anyone.
	ErrLastAdmin = errors.New("cannot remove the last admin")

	// ErrInsufficientCredits is returned instead of starting a turn the
	// tenant cannot pay for (T-03). It is distinct from ErrUnauthorized
	// because it maps to 402 rather than 403, and because every channel
	// answers it with a plain sentence rather than a rejection.
	ErrInsufficientCredits = errors.New("insufficient credits")

	// ErrActionExpired is returned when a proposed action is decided on after
	// its 24h window has passed (T-10). Distinct from ErrConflict because the
	// proposal was valid to approve until it timed out — the caller did nothing
	// wrong, the world just moved on — so the message is "this proposal has
	// expired; ask the agent to propose it again" rather than a flat refusal.
	ErrActionExpired = errors.New("action proposal expired")
)
