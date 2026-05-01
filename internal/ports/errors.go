package ports

import "errors"

// ErrNotFound is returned by repositories when a lookup misses. Callers MUST
// match with errors.Is rather than equality so wrapping is permitted.
var ErrNotFound = errors.New("ports: not found")

// ErrConflict is returned when an idempotent write detects a state mismatch
// (e.g. Save with a stale Run version, Register with an existing IntentType).
var ErrConflict = errors.New("ports: conflict")
