package domain

import "errors"

// Sentinel errors are deliberately small and transport agnostic.  The HTTP
// layer can map them to status codes without making the domain depend on a
// particular web framework or database driver.
var (
	ErrInvalid   = errors.New("invalid domain value")
	ErrNotFound  = errors.New("domain object not found")
	ErrConflict  = errors.New("domain conflict")
	ErrImmutable = errors.New("domain object is immutable")
	ErrForbidden = errors.New("domain operation forbidden")
)
