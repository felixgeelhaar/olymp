// Package domain holds the pure domain types of Olymp.
//
// No imports outside the standard library, github.com/google/uuid, and
// github.com/felixgeelhaar/statekit are permitted here. This is the runtime's
// vocabulary: Run, Intent, Goal, Provenance, RunEvent, and the cross-layer
// references that compose into the cognitive loop.
package domain

import "time"

// CallerRef identifies who initiated or steers a Run. Mirrors the shape used
// across the cognitive stack so audit vocabulary stays consistent.
type CallerRef struct {
	Type string `json:"type"` // "agent" | "user" | "scheduler" | "plugin"
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Tenant is the org/team/user scoping for multi-tenant runtimes. The empty
// Tenant{} is the single-tenant default — equivalent to "no tenancy".
type Tenant struct {
	Org  string `json:"org,omitempty"`
	Team string `json:"team,omitempty"`
	User string `json:"user,omitempty"`
}

// Key returns a stable concatenation suitable for filter equality + indexing.
func (t Tenant) Key() string {
	if t.Org == "" && t.Team == "" && t.User == "" {
		return ""
	}
	return t.Org + "|" + t.Team + "|" + t.User
}

// IsZero reports whether the Tenant is unset (single-tenant mode).
func (t Tenant) IsZero() bool { return t.Org == "" && t.Team == "" && t.User == "" }

// SessionRef identifies the session a Run belongs to.
type SessionRef struct {
	ID string `json:"id"`
}

// Session is a long-lived correlation context across runs by the same caller.
type Session struct {
	ID        string         `json:"id"`
	Tenant    Tenant         `json:"tenant,omitempty"`
	Caller    CallerRef      `json:"caller"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// IntentType describes a registered, typed intent.
type IntentType struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	Schema       map[string]any `json:"schema"` // JSON Schema for Intent.Payload
	Policy       IntentPolicy   `json:"policy"`
	RegisteredAt time.Time      `json:"registered_at"`
}

// IntentPolicy is the per-intent loop policy applied at Submit.
type IntentPolicy struct {
	MaxIterations   int           `json:"max_iterations"`
	DefaultDeadline time.Duration `json:"default_deadline,omitempty"`
	RequiredScopes  []string      `json:"required_scopes,omitempty"`
	RequireApproval bool          `json:"require_approval"`
	ReadOnly        bool          `json:"read_only"` // when true, Praxis.Execute is forbidden
}

// Intent is a typed request submitted to the runtime.
type Intent struct {
	Type    string         `json:"type"`              // matches IntentType.Name
	Subject string         `json:"subject,omitempty"` // entity the intent is about
	Payload map[string]any `json:"payload,omitempty"`
}

// GoalCriterion is one structured success condition for a Run.
type GoalCriterion struct {
	Kind     string `json:"kind"` // "signal_resolved" | "claim_confirmed" | "action_succeeded" | "user_accepted"
	Subject  string `json:"subject"`
	Operator string `json:"operator"` // "eq" | "lte" | "gte" | ...
	Value    any    `json:"value,omitempty"`
}

// Goal bounds and defines success for a Run. Immutable per Run.
type Goal struct {
	Description   string          `json:"description"`
	Criteria      []GoalCriterion `json:"criteria,omitempty"`
	MaxIterations int             `json:"max_iterations"`
	Deadline      *time.Time      `json:"deadline,omitempty"`
}

// LayerRef is a cross-layer pointer recorded in provenance.
type LayerRef struct {
	Layer string `json:"layer"`        // "mnemos" | "chronos" | "nous" | "praxis" | "olymp"
	ID    string `json:"id,omitempty"` // remote object id
}

// ProvenanceStep is one stage of one iteration of the loop.
type ProvenanceStep struct {
	Iteration   int            `json:"iteration"`
	Stage       RunStatus      `json:"stage"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt time.Time      `json:"completed_at"`
	Inputs      map[string]any `json:"inputs,omitempty"`
	Outputs     map[string]any `json:"outputs,omitempty"`
	LayerRef    LayerRef       `json:"layer_ref"`
	Error       *RunError      `json:"error,omitempty"`
}

// Provenance accumulates every step taken across the loop's iterations.
type Provenance struct {
	Steps []ProvenanceStep `json:"steps,omitempty"`
}

// RunError is a structured failure attached to a Run or a ProvenanceStep.
type RunError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Layer     string         `json:"layer,omitempty"`
	Cause     map[string]any `json:"cause,omitempty"`
	Retryable bool           `json:"retryable"`
}

// SteerCommand is a runtime-level instruction issued against a live Run.
type SteerCommand struct {
	Kind   string    `json:"kind"` // "pause" | "resume" | "cancel" | "approve" | "deny"
	Reason string    `json:"reason,omitempty"`
	Caller CallerRef `json:"caller"`
}

// RunFilter narrows queries against the run registry and the event stream.
type RunFilter struct {
	RunID      string    `json:"run_id,omitempty"`
	IntentType string    `json:"intent_type,omitempty"`
	CallerType string    `json:"caller_type,omitempty"`
	CallerID   string    `json:"caller_id,omitempty"`
	Status     RunStatus `json:"status,omitempty"`
	Tenant     Tenant    `json:"tenant,omitempty"`
	Limit      int       `json:"limit,omitempty"`
}

// SessionFilter narrows queries against the session repository.
type SessionFilter struct {
	CallerType string `json:"caller_type,omitempty"`
	CallerID   string `json:"caller_id,omitempty"`
	Tenant     Tenant `json:"tenant,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// RunEvent is the unit emitted by Stream.
type RunEvent struct {
	RunID     string         `json:"run_id"`
	Iteration int            `json:"iteration"`
	Stage     RunStatus      `json:"stage,omitempty"`
	Kind      string         `json:"kind"`
	Payload   map[string]any `json:"payload,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// AuditEvent is one durable record on the audit trail.
type AuditEvent struct {
	ID        string         `json:"id"`
	RunID     string         `json:"run_id"`
	Kind      string         `json:"kind"` // "submitted", "transitioned", "steered", ...
	Detail    map[string]any `json:"detail,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// AuditQuery filters the audit trail.
type AuditQuery struct {
	RunID string    `json:"run_id,omitempty"`
	Kind  string    `json:"kind,omitempty"`
	Since time.Time `json:"since,omitempty"`
	Until time.Time `json:"until,omitempty"`
	Limit int       `json:"limit,omitempty"`
}

// MemoryRef is a Mnemos pointer carried through the loop.
type MemoryRef struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// MemoryQuery is a Mnemos.Recall request shape.
type MemoryQuery struct {
	RunID   string         `json:"run_id"`
	Goal    Goal           `json:"goal"`
	Session SessionRef     `json:"session"`
	Filter  map[string]any `json:"filter,omitempty"`
	Limit   int            `json:"limit,omitempty"`
}

// SignalRef is a Chronos pointer carried through the loop.
type SignalRef struct {
	ID         string         `json:"id"`
	Pattern    string         `json:"pattern,omitempty"`
	Strength   float64        `json:"strength,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// SignalQuery is a Chronos.Signals request shape.
type SignalQuery struct {
	RunID   string         `json:"run_id"`
	Goal    Goal           `json:"goal"`
	Session SessionRef     `json:"session"`
	Since   time.Time      `json:"since,omitempty"`
	Filter  map[string]any `json:"filter,omitempty"`
	Limit   int            `json:"limit,omitempty"`
}

// DecisionRef is a Nous pointer carried through the loop.
type DecisionRef struct {
	ID               string          `json:"id"`
	RequiresApproval bool            `json:"requires_approval"`
	Actions          []ActionRequest `json:"actions,omitempty"`
	Rationale        string          `json:"rationale,omitempty"`
	Metadata         map[string]any  `json:"metadata,omitempty"`
}

// DecisionRequest is a Nous.Decide request shape assembled by the loop.
type DecisionRequest struct {
	RunID    string      `json:"run_id"`
	Goal     Goal        `json:"goal"`
	Memories []MemoryRef `json:"memories,omitempty"`
	Signals  []SignalRef `json:"signals,omitempty"`
	History  Provenance  `json:"history"`
}

// CapabilityRef is a Praxis capability descriptor.
type CapabilityRef struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Simulatable bool   `json:"simulatable"`
	Idempotent  bool   `json:"idempotent"`
}

// ActionRequest is a Praxis.Execute / DryRun input shape.
type ActionRequest struct {
	ID             string         `json:"id"`
	Capability     string         `json:"capability"`
	Payload        map[string]any `json:"payload,omitempty"`
	Caller         CallerRef      `json:"caller"`
	Scope          []string       `json:"scope,omitempty"`
	IdempotencyKey string         `json:"idempotency_key"`
	Metadata       map[string]any `json:"metadata,omitempty"` // carries run_id, iteration, decision_id
	Retryable      bool           `json:"retryable"`
}

// ActionResult is the terminal output of a Praxis.Execute call.
type ActionResult struct {
	ActionID    string         `json:"action_id"`
	Status      string         `json:"status"` // "succeeded" | "failed" | ...
	Output      map[string]any `json:"output,omitempty"`
	ExternalID  string         `json:"external_id,omitempty"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt time.Time      `json:"completed_at"`
	Attempts    int            `json:"attempts"`
	Error       *RunError      `json:"error,omitempty"`
}

// SimulationRef is the output of a Praxis.DryRun call.
type SimulationRef struct {
	ActionID   string         `json:"action_id"`
	Preview    map[string]any `json:"preview,omitempty"`
	Reversible bool           `json:"reversible"`
}

// ActionRef is a stored pointer to an action belonging to a Run.
type ActionRef struct {
	ID         string `json:"id"`
	Capability string `json:"capability"`
	Status     string `json:"status"`
}

// OutcomeRef is a Mnemos event written by the learning stage.
type OutcomeRef struct {
	ID       string `json:"id"`
	ActionID string `json:"action_id"`
	Status   string `json:"status"`
}

// OutcomeEvent is the structured payload Olymp writes back into Mnemos at the
// learning stage of every iteration.
type OutcomeEvent struct {
	Type       string    `json:"type"` // always "olymp.outcome"
	RunID      string    `json:"run_id"`
	Iteration  int       `json:"iteration"`
	Intent     Intent    `json:"intent"`
	ActionID   string    `json:"action_id"`
	Capability string    `json:"capability"`
	Status     string    `json:"status"`
	DecisionID string    `json:"decision_id,omitempty"`
	MemoryRefs []string  `json:"memory_refs,omitempty"`
	SignalRefs []string  `json:"signal_refs,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// ApprovalRequest is raised when a Run enters awaiting_approval (Phase 2).
type ApprovalRequest struct {
	RunID      string    `json:"run_id"`
	DecisionID string    `json:"decision_id"`
	RequiredAt time.Time `json:"required_at"`
	Reason     string    `json:"reason,omitempty"`
}

// ApprovalDecision resolves an ApprovalRequest (Phase 2).
type ApprovalDecision struct {
	Decision   string    `json:"decision"` // "approve" | "deny"
	Reason     string    `json:"reason,omitempty"`
	Resolver   CallerRef `json:"resolver"`
	ResolvedAt time.Time `json:"resolved_at"`
}
