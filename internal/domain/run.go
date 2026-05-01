package domain

import (
	"errors"
	"time"
)

// RunStatus is the discriminator on the Run lifecycle FSM.
type RunStatus string

const (
	StatusPending          RunStatus = "pending"
	StatusObserving        RunStatus = "observing"
	StatusUnderstanding    RunStatus = "understanding"
	StatusDeciding         RunStatus = "deciding"
	StatusAwaitingApproval RunStatus = "awaiting_approval"
	StatusActing           RunStatus = "acting"
	StatusLearning         RunStatus = "learning"
	StatusCompleted        RunStatus = "completed"
	StatusFailed           RunStatus = "failed"
	StatusCancelled        RunStatus = "cancelled"
	StatusPaused           RunStatus = "paused"
)

// IsTerminal reports whether s is a final state of the Run FSM.
func (s RunStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// String returns the wire form of the status.
func (s RunStatus) String() string { return string(s) }

// Run is the runtime aggregate. It carries the goal, the FSM cursor, and the
// accumulating provenance for one closed-loop execution.
type Run struct {
	ID         string     `json:"id"`
	Tenant     Tenant     `json:"tenant,omitempty"`
	Intent     Intent     `json:"intent"`
	Session    SessionRef `json:"session"`
	Caller     CallerRef  `json:"caller"`
	Scope      []string   `json:"scope,omitempty"`
	Status     RunStatus  `json:"status"`
	Iteration  int        `json:"iteration"`
	Goal       Goal       `json:"goal"`
	Provenance Provenance `json:"provenance"`
	LastError  *RunError  `json:"last_error,omitempty"`
	// PendingDecision is set when the run is paused at awaiting_approval.
	// On Resume the engine reads this back and dispatches to the acting stage.
	PendingDecision *DecisionRef `json:"pending_decision,omitempty"`
	StartedAt       time.Time    `json:"started_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
	CompletedAt     *time.Time   `json:"completed_at,omitempty"`
}

// Validate enforces the Run aggregate's invariants. Repository implementations
// MUST call Validate before persisting.
func (r Run) Validate() error {
	switch {
	case r.ID == "":
		return errors.New("run: id is required")
	case r.Intent.Type == "":
		return errors.New("run: intent.type is required")
	case r.Session.ID == "":
		return errors.New("run: session.id is required")
	case r.Caller.Type == "" || r.Caller.ID == "":
		return errors.New("run: caller.type and caller.id are required")
	case r.Status == "":
		return errors.New("run: status is required")
	case r.Goal.MaxIterations <= 0:
		return errors.New("run: goal.max_iterations must be > 0")
	case r.Iteration < 0:
		return errors.New("run: iteration must be >= 0")
	}
	if !validStatus[r.Status] {
		return errors.New("run: unknown status: " + string(r.Status))
	}
	if r.Status.IsTerminal() && r.CompletedAt == nil {
		return errors.New("run: terminal status requires completed_at")
	}
	return nil
}

// AppendProvenance appends a step and bumps UpdatedAt. The Run does not
// validate the step itself — that is the loop engine's responsibility.
func (r *Run) AppendProvenance(step ProvenanceStep) {
	r.Provenance.Steps = append(r.Provenance.Steps, step)
	r.UpdatedAt = step.CompletedAt
}

// Complete marks a Run terminal with the given status and timestamp.
// It returns an error if status is not one of the terminal states.
func (r *Run) Complete(status RunStatus, at time.Time) error {
	if !status.IsTerminal() {
		return errors.New("run: complete requires a terminal status")
	}
	r.Status = status
	r.UpdatedAt = at
	t := at
	r.CompletedAt = &t
	return nil
}

// RunSnapshot is the assembled view returned by Inspect. It composes the
// stored Run with cross-layer references resolved at read time.
type RunSnapshot struct {
	Run       Run              `json:"run"`
	Memories  []MemoryRef      `json:"memories,omitempty"`
	Signals   []SignalRef      `json:"signals,omitempty"`
	Decisions []DecisionRef    `json:"decisions,omitempty"`
	Actions   []ActionRef      `json:"actions,omitempty"`
	Outcomes  []OutcomeRef     `json:"outcomes,omitempty"`
	Timeline  []ProvenanceStep `json:"timeline,omitempty"`
}

var validStatus = map[RunStatus]bool{
	StatusPending:          true,
	StatusObserving:        true,
	StatusUnderstanding:    true,
	StatusDeciding:         true,
	StatusAwaitingApproval: true,
	StatusActing:           true,
	StatusLearning:         true,
	StatusCompleted:        true,
	StatusFailed:           true,
	StatusCancelled:        true,
	StatusPaused:           true,
}
