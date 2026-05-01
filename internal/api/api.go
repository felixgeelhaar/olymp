// Package api is the public surface of the Olymp runtime.
//
// It defines OlympAPI (Submit / Inspect / Steer / Stream) and the Service
// that satisfies it by composing engine + repos + bus + registry. The HTTP
// transport in http.go binds Service to /v1/runs.
package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/felixgeelhaar/olymp/internal/audit"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/engine"
	"github.com/felixgeelhaar/olymp/internal/eventbus"
	"github.com/felixgeelhaar/olymp/internal/intent"
	"github.com/felixgeelhaar/olymp/internal/ports"

	"github.com/google/uuid"
)

// OlympAPI is the four-verb public contract of the runtime.
type OlympAPI interface {
	Submit(ctx context.Context, in domain.Intent) (domain.Run, error)
	Inspect(ctx context.Context, runID string) (domain.RunSnapshot, error)
	Steer(ctx context.Context, runID string, command domain.SteerCommand) error
	Stream(ctx context.Context, filter domain.RunFilter) (<-chan domain.RunEvent, error)
}

// Service is the production implementation of OlympAPI.
type Service struct {
	repos    ports.Repos
	registry *intent.Registry
	auditor  *audit.Logger
	bus      *eventbus.Bus
	engine   *engine.Engine
	now      func() time.Time
}

// NewService wires a Service. All deps are required.
func NewService(repos ports.Repos, registry *intent.Registry, auditor *audit.Logger, bus *eventbus.Bus, engine *engine.Engine) *Service {
	return &Service{
		repos: repos, registry: registry, auditor: auditor, bus: bus, engine: engine,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// Submit validates the intent, materialises a Run, and runs the loop. The
// loop is synchronous in Phase 1; an async/queue-backed scheduler is the
// Phase-3 distributed-runner task.
func (s *Service) Submit(ctx context.Context, in domain.Intent) (domain.Run, error) {
	t, err := s.registry.Validate(ctx, in)
	if err != nil {
		return domain.Run{}, err
	}
	caller := callerFromContext(ctx)
	tenant := TenantFromContext(ctx)
	session, err := s.resolveSession(ctx, caller, tenant)
	if err != nil {
		return domain.Run{}, err
	}
	run := domain.Run{
		ID:        uuid.NewString(),
		Tenant:    tenant,
		Intent:    in,
		Session:   domain.SessionRef{ID: session.ID},
		Caller:    caller,
		Status:    domain.StatusPending,
		Goal:      intent.PreparePolicy(t, domain.Goal{Description: in.Type + ":" + intent.SubjectOf(in)}),
		StartedAt: s.now(),
		UpdatedAt: s.now(),
	}
	if err := s.repos.Runs.Save(ctx, run); err != nil {
		return domain.Run{}, fmt.Errorf("submit: persist: %w", err)
	}
	if s.auditor != nil {
		_ = s.auditor.Submitted(ctx, run)
	}
	if s.bus != nil {
		s.bus.Publish(domain.RunEvent{
			RunID: run.ID, Kind: "submitted", Timestamp: s.now(),
			Payload: map[string]any{"intent_type": in.Type},
		})
	}
	final, err := s.engine.Run(ctx, run)
	if err != nil {
		return final, err
	}
	return final, nil
}

// Inspect assembles a RunSnapshot from the stored Run + provenance. Cross-
// layer references are populated from the run's provenance steps; resolving
// them against live layers is the responsibility of the caller.
func (s *Service) Inspect(ctx context.Context, runID string) (domain.RunSnapshot, error) {
	run, err := s.repos.Runs.Get(ctx, runID)
	if err != nil {
		return domain.RunSnapshot{}, err
	}
	if t := TenantFromContext(ctx); !t.IsZero() && run.Tenant.Key() != t.Key() {
		return domain.RunSnapshot{}, fmt.Errorf("inspect: %w", ports.ErrNotFound)
	}
	snap := domain.RunSnapshot{Run: run, Timeline: run.Provenance.Steps}
	for _, step := range run.Provenance.Steps {
		switch step.LayerRef.Layer {
		case "mnemos":
			if step.Stage == domain.StatusObserving && step.LayerRef.ID != "" {
				snap.Memories = append(snap.Memories, domain.MemoryRef{ID: step.LayerRef.ID})
			}
			if step.Stage == domain.StatusLearning && step.LayerRef.ID != "" {
				snap.Outcomes = append(snap.Outcomes, domain.OutcomeRef{ID: step.LayerRef.ID})
			}
		case "chronos":
			if step.LayerRef.ID != "" {
				snap.Signals = append(snap.Signals, domain.SignalRef{ID: step.LayerRef.ID})
			}
		case "nous":
			if step.LayerRef.ID != "" {
				snap.Decisions = append(snap.Decisions, domain.DecisionRef{ID: step.LayerRef.ID})
			}
		case "praxis":
			if step.LayerRef.ID != "" {
				snap.Actions = append(snap.Actions, domain.ActionRef{ID: step.LayerRef.ID})
			}
		}
	}
	return snap, nil
}

// Steer applies a runtime command to a live (or paused) Run.
func (s *Service) Steer(ctx context.Context, runID string, cmd domain.SteerCommand) error {
	run, err := s.repos.Runs.Get(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status.IsTerminal() {
		return fmt.Errorf("steer: run %s is %s", runID, run.Status)
	}
	switch cmd.Kind {
	case "pause":
		if err := s.repos.Runs.UpdateStatus(ctx, runID, domain.StatusPaused); err != nil {
			return err
		}
	case "cancel":
		if err := s.repos.Runs.UpdateStatus(ctx, runID, domain.StatusCancelled); err != nil {
			return err
		}
	case "approve":
		if s.repos.Approvals != nil {
			if err := s.repos.Approvals.Resolve(ctx, runID, domain.ApprovalDecision{
				Decision:   "approve",
				Reason:     cmd.Reason,
				Resolver:   cmd.Caller,
				ResolvedAt: s.now(),
			}); err != nil {
				return fmt.Errorf("steer approve: resolve: %w", err)
			}
		}
		// Resume the run: re-enter the loop. The engine detects the
		// awaiting_approval state and the resolved approval, jumps to acting
		// with the persisted PendingDecision, and continues.
		if run.Status == domain.StatusAwaitingApproval {
			go func() {
				if _, err := s.engine.Resume(context.Background(), runID); err != nil {
					if s.auditor != nil {
						_ = s.auditor.Failed(context.Background(), runID, &domain.RunError{
							Code: "resume_failed", Message: err.Error(), Layer: "olymp",
						})
					}
				}
			}()
		}
	case "deny":
		if s.repos.Approvals != nil {
			_ = s.repos.Approvals.Resolve(ctx, runID, domain.ApprovalDecision{
				Decision:   "deny",
				Reason:     cmd.Reason,
				Resolver:   cmd.Caller,
				ResolvedAt: s.now(),
			})
		}
		_ = s.repos.Runs.UpdateStatus(ctx, runID, domain.StatusCancelled)
	case "resume":
		// Phase 2 hardens; for Phase 1 a resume from paused → observing.
		_ = s.repos.Runs.UpdateStatus(ctx, runID, domain.StatusObserving)
	default:
		return fmt.Errorf("steer: unknown command kind %q", cmd.Kind)
	}
	if s.auditor != nil {
		_ = s.auditor.Steered(ctx, runID, cmd)
	}
	if s.bus != nil {
		s.bus.Publish(domain.RunEvent{
			RunID: runID, Kind: "steered", Timestamp: s.now(),
			Payload: map[string]any{"command": cmd.Kind, "reason": cmd.Reason},
		})
	}
	return nil
}

// Stream returns a channel of RunEvents matching filter. Closed when ctx is
// cancelled. nil when bus is unset.
func (s *Service) Stream(ctx context.Context, filter domain.RunFilter) (<-chan domain.RunEvent, error) {
	if s.bus == nil {
		return nil, errors.New("stream: bus not configured")
	}
	return s.bus.Subscribe(ctx, filter), nil
}

// Halt is the runtime kill-switch. It pauses every non-terminal run and
// denies pending approvals. Returns the IDs of affected runs.
func (s *Service) Halt(ctx context.Context, reason string) ([]string, error) {
	caller := callerFromContext(ctx)
	return s.engine.Halt(ctx, reason, caller)
}

// resolveSession ensures the caller has a Session, scoped to the tenant.
// Sessions are keyed by (tenant, caller) so the same identity in different
// tenants stays isolated.
func (s *Service) resolveSession(ctx context.Context, caller domain.CallerRef, tenant domain.Tenant) (domain.Session, error) {
	id := strings.TrimSpace(tenant.Key() + ":" + caller.Type + ":" + caller.ID)
	if existing, err := s.repos.Sessions.Get(ctx, id); err == nil {
		return existing, nil
	}
	session := domain.Session{
		ID: id, Tenant: tenant, Caller: caller,
		CreatedAt: s.now(), UpdatedAt: s.now(),
	}
	if err := s.repos.Sessions.Upsert(ctx, session); err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

type callerKey struct{}
type tenantKey struct{}

// WithCaller stamps a CallerRef onto ctx; transports use this to forward the
// authenticated caller into the Service.
func WithCaller(ctx context.Context, c domain.CallerRef) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// WithTenant stamps a Tenant onto ctx; transports use this to forward the
// authenticated tenant into the Service.
func WithTenant(ctx context.Context, t domain.Tenant) context.Context {
	return context.WithValue(ctx, tenantKey{}, t)
}

func callerFromContext(ctx context.Context) domain.CallerRef {
	if v, ok := ctx.Value(callerKey{}).(domain.CallerRef); ok && v.ID != "" {
		return v
	}
	return domain.CallerRef{Type: "user", ID: "anonymous"}
}

// TenantFromContext returns the tenant on ctx, or the zero Tenant for
// single-tenant deployments.
func TenantFromContext(ctx context.Context) domain.Tenant {
	if v, ok := ctx.Value(tenantKey{}).(domain.Tenant); ok {
		return v
	}
	return domain.Tenant{}
}
