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
)
