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

	// The top-up refusals (RIZ-83). Each is a 409 with its own sentence, and
	// they are separate sentinels rather than ErrConflict with a message
	// because the handler's copy is the product's, not the repository's.
	//
	// ErrTopupInProgress: the workspace already has an open request — the
	// one-open-per-company partial index refused the insert.
	ErrTopupInProgress = errors.New("a top-up is already in progress")
	// ErrTopupCodeTaken: another open request already uses this exact
	// expected amount. Never reaches a caller: the service draws another code.
	ErrTopupCodeTaken = errors.New("the top-up matching code is taken")
	// ErrTopupClosed: proof sent to a request that is no longer open.
	ErrTopupClosed = errors.New("the top-up is closed")
	// ErrTopupNotCancellable: only a request still waiting for payment can be
	// cancelled; one with proof is the operator's to decide.
	ErrTopupNotCancellable = errors.New("only a top-up waiting for payment can be cancelled")
	// ErrTopupAlreadyReviewed: approve or reject on a request that is not in
	// review — already decided, by this operator's other click or another's.
	ErrTopupAlreadyReviewed = errors.New("the top-up was already reviewed")

	// ErrCreditGrantAlreadyReviewed: approve or reject on a free-credit request
	// that is no longer pending (RIZ-89) — decided by this operator's other
	// click or by another operator. A 409, for ErrTopupAlreadyReviewed's reason.
	ErrCreditGrantAlreadyReviewed = errors.New("the free-credit request was already reviewed")

	// The chat model picker's two refusals (RIZ-97). Sentinels rather than
	// ErrInvalidInput / ErrConflict, for the top-ups' reason: each carries a
	// `code` the dashboard can tell apart, and the sentence is the handler's.
	//
	// ErrModelNotOffered: the model is not on the workspace's allow-list. A 400.
	ErrModelNotOffered = errors.New("chat model is not offered")
	// ErrOwnProvider: the workspace's primary row carries its own provider, so
	// its model is support's to change. A 409.
	ErrOwnProvider = errors.New("workspace runs on its own model provider")
)
