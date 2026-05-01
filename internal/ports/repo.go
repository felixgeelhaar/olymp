// Package ports declares the repository contracts the runtime depends on.
//
// Domain code in `internal/engine`, `internal/api`, and `cmd/olymp` MUST
// import only this package, never a concrete backend. Backends in
// `internal/store/<name>` implement these interfaces and are selected at
// startup via `internal/store.Open`.
package ports

import (
	"context"

	"github.com/felixgeelhaar/olymp/internal/domain"
)

// RunRepo persists and queries the Run aggregate.
type RunRepo interface {
	Save(ctx context.Context, r domain.Run) error
	Get(ctx context.Context, id string) (domain.Run, error)
	UpdateStatus(ctx context.Context, id string, status domain.RunStatus) error
	AppendProvenance(ctx context.Context, id string, step domain.ProvenanceStep) error
	List(ctx context.Context, filter domain.RunFilter) ([]domain.Run, error)
}

// SessionRepo persists and queries Sessions.
type SessionRepo interface {
	Upsert(ctx context.Context, s domain.Session) error
	Get(ctx context.Context, id string) (domain.Session, error)
	List(ctx context.Context, filter domain.SessionFilter) ([]domain.Session, error)
}

// IntentTypeRepo persists registered IntentTypes.
type IntentTypeRepo interface {
	Register(ctx context.Context, t domain.IntentType) error
	Get(ctx context.Context, name string) (domain.IntentType, error)
	List(ctx context.Context) ([]domain.IntentType, error)
}

// AuditRepo is the append-only audit trail of run lifecycle events.
type AuditRepo interface {
	Append(ctx context.Context, e domain.AuditEvent) error
	ListForRun(ctx context.Context, runID string) ([]domain.AuditEvent, error)
	Search(ctx context.Context, q domain.AuditQuery) ([]domain.AuditEvent, error)
}

// ApprovalRepo persists pending approvals (Phase 2). Implementations may
// return ErrNotFound for a run with no outstanding approval.
type ApprovalRepo interface {
	Pending(ctx context.Context, runID string) (*domain.ApprovalRequest, error)
	Raise(ctx context.Context, runID string, req domain.ApprovalRequest) error
	Resolve(ctx context.Context, runID string, decision domain.ApprovalDecision) error
}

// Repos bundles every repository for a single backend.
//
// Open() in `internal/store` returns a Repos so the runtime never has to know
// which backend it is talking to.
type Repos struct {
	Runs        RunRepo
	Sessions    SessionRepo
	IntentTypes IntentTypeRepo
	Audit       AuditRepo
	Approvals   ApprovalRepo
	Close       func() error
}
