// Package memory is the in-process reference implementation of every
// repository port. It is the canonical store: SQL backends in
// internal/store/sqlite and internal/store/postgres re-run the same shared
// suite (see internal/store/suite) and must round-trip everything memory
// passes.
package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// New returns a fresh in-memory ports.Repos. Safe for concurrent use.
func New() ports.Repos {
	return ports.Repos{
		Runs:        &runRepo{runs: map[string]domain.Run{}},
		Sessions:    &sessionRepo{sessions: map[string]domain.Session{}},
		IntentTypes: &intentTypeRepo{types: map[string]domain.IntentType{}},
		Audit:       &auditRepo{events: map[string][]domain.AuditEvent{}},
		Approvals:   &approvalRepo{pending: map[string]domain.ApprovalRequest{}},
		Close:       func() error { return nil },
	}
}

type runRepo struct {
	mu   sync.RWMutex
	runs map[string]domain.Run
}

func (r *runRepo) Save(_ context.Context, run domain.Run) error {
	if err := run.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[run.ID] = cloneRun(run)
	return nil
}

func (r *runRepo) Get(_ context.Context, id string) (domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	run, ok := r.runs[id]
	if !ok {
		return domain.Run{}, fmt.Errorf("run %q: %w", id, ports.ErrNotFound)
	}
	return cloneRun(run), nil
}

func (r *runRepo) UpdateStatus(_ context.Context, id string, status domain.RunStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok {
		return fmt.Errorf("run %q: %w", id, ports.ErrNotFound)
	}
	run.Status = status
	r.runs[id] = run
	return nil
}

func (r *runRepo) AppendProvenance(_ context.Context, id string, step domain.ProvenanceStep) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok {
		return fmt.Errorf("run %q: %w", id, ports.ErrNotFound)
	}
	run.AppendProvenance(step)
	r.runs[id] = run
	return nil
}

func (r *runRepo) List(_ context.Context, filter domain.RunFilter) ([]domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.Run, 0, len(r.runs))
	for _, run := range r.runs {
		if !matchesRun(run, filter) {
			continue
		}
		out = append(out, cloneRun(run))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func matchesRun(r domain.Run, f domain.RunFilter) bool {
	if f.RunID != "" && r.ID != f.RunID {
		return false
	}
	if f.IntentType != "" && r.Intent.Type != f.IntentType {
		return false
	}
	if f.CallerType != "" && r.Caller.Type != f.CallerType {
		return false
	}
	if f.CallerID != "" && r.Caller.ID != f.CallerID {
		return false
	}
	if f.Status != "" && r.Status != f.Status {
		return false
	}
	if !f.Tenant.IsZero() && r.Tenant.Key() != f.Tenant.Key() {
		return false
	}
	return true
}

type sessionRepo struct {
	mu       sync.RWMutex
	sessions map[string]domain.Session
}

func (r *sessionRepo) Upsert(_ context.Context, s domain.Session) error {
	if s.ID == "" {
		return fmt.Errorf("session: id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.ID] = s
	return nil
}

func (r *sessionRepo) Get(_ context.Context, id string) (domain.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[id]
	if !ok {
		return domain.Session{}, fmt.Errorf("session %q: %w", id, ports.ErrNotFound)
	}
	return s, nil
}

func (r *sessionRepo) List(_ context.Context, filter domain.SessionFilter) ([]domain.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		if filter.CallerType != "" && s.Caller.Type != filter.CallerType {
			continue
		}
		if filter.CallerID != "" && s.Caller.ID != filter.CallerID {
			continue
		}
		if !filter.Tenant.IsZero() && s.Tenant.Key() != filter.Tenant.Key() {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

type intentTypeRepo struct {
	mu    sync.RWMutex
	types map[string]domain.IntentType
}

func (r *intentTypeRepo) Register(_ context.Context, t domain.IntentType) error {
	if t.Name == "" {
		return fmt.Errorf("intent type: name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.types[t.Name] = t
	return nil
}

func (r *intentTypeRepo) Get(_ context.Context, name string) (domain.IntentType, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.types[name]
	if !ok {
		return domain.IntentType{}, fmt.Errorf("intent type %q: %w", name, ports.ErrNotFound)
	}
	return t, nil
}

func (r *intentTypeRepo) List(_ context.Context) ([]domain.IntentType, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.IntentType, 0, len(r.types))
	for _, t := range r.types {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

type auditRepo struct {
	mu     sync.RWMutex
	events map[string][]domain.AuditEvent // run_id → events
}

func (r *auditRepo) Append(_ context.Context, e domain.AuditEvent) error {
	if e.RunID == "" {
		return fmt.Errorf("audit: run_id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events[e.RunID] = append(r.events[e.RunID], e)
	return nil
}

func (r *auditRepo) ListForRun(_ context.Context, runID string) ([]domain.AuditEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	events := append([]domain.AuditEvent(nil), r.events[runID]...)
	sort.Slice(events, func(i, j int) bool { return events[i].CreatedAt.Before(events[j].CreatedAt) })
	return events, nil
}

func (r *auditRepo) Search(_ context.Context, q domain.AuditQuery) ([]domain.AuditEvent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []domain.AuditEvent
	for runID, events := range r.events {
		if q.RunID != "" && runID != q.RunID {
			continue
		}
		for _, e := range events {
			if q.Kind != "" && e.Kind != q.Kind {
				continue
			}
			if !q.Since.IsZero() && e.CreatedAt.Before(q.Since) {
				continue
			}
			if !q.Until.IsZero() && e.CreatedAt.After(q.Until) {
				continue
			}
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

type approvalRepo struct {
	mu       sync.RWMutex
	pending  map[string]domain.ApprovalRequest
	resolved map[string]domain.ApprovalDecision
}

func (r *approvalRepo) Pending(_ context.Context, runID string) (*domain.ApprovalRequest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	req, ok := r.pending[runID]
	if !ok {
		return nil, fmt.Errorf("approval %q: %w", runID, ports.ErrNotFound)
	}
	out := req
	return &out, nil
}

func (r *approvalRepo) Raise(_ context.Context, runID string, req domain.ApprovalRequest) error {
	if runID == "" {
		return fmt.Errorf("approval: run_id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[runID] = req
	return nil
}

func (r *approvalRepo) Resolve(_ context.Context, runID string, decision domain.ApprovalDecision) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pending[runID]; !ok {
		return fmt.Errorf("approval %q: %w", runID, ports.ErrNotFound)
	}
	delete(r.pending, runID)
	if r.resolved == nil {
		r.resolved = map[string]domain.ApprovalDecision{}
	}
	r.resolved[runID] = decision
	return nil
}

// cloneRun returns a deep-enough copy that callers can mutate the result
// without racing with the store. Slices and maps are duplicated; nested
// pointers are shared (RunError, time pointers are immutable in practice).
func cloneRun(r domain.Run) domain.Run {
	out := r
	if r.Scope != nil {
		out.Scope = append([]string(nil), r.Scope...)
	}
	if r.Provenance.Steps != nil {
		out.Provenance.Steps = append([]domain.ProvenanceStep(nil), r.Provenance.Steps...)
	}
	if r.Goal.Criteria != nil {
		out.Goal.Criteria = append([]domain.GoalCriterion(nil), r.Goal.Criteria...)
	}
	// Tenant is a value type; PendingDecision is a pointer to immutable refs
	// in practice — no deep clone required.
	return out
}
