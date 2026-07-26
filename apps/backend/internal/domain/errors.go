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
)
