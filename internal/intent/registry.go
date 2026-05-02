// Package intent is the typed intent registry.
//
// Submit/Inspect/Steer/Stream all flow through registered IntentTypes. The
// registry validates payloads against the type's JSON Schema and surfaces
// the type's loop policy (max iterations, default deadline, required scopes,
// approval rules, read-only flag).
//
// Built-in types ship with `explain` and `remediate` (see Builtins). Hosts
// register additional types at startup via Register.
package intent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// ErrUnknownType is returned by Validate when the intent's Type is not
// registered.
var ErrUnknownType = errors.New("intent: unknown type")

// ErrValidation is returned for payload-shape mismatches.
type ErrValidation struct {
	Field  string
	Reason string
}

func (e *ErrValidation) Error() string {
	if e.Field == "" {
		return "intent: " + e.Reason
	}
	return fmt.Sprintf("intent: %s: %s", e.Field, e.Reason)
}

// Registry is a thin facade over ports.IntentTypeRepo. Construct with New.
type Registry struct {
	repo ports.IntentTypeRepo
}

// New returns a Registry backed by repo.
func New(repo ports.IntentTypeRepo) *Registry {
	return &Registry{repo: repo}
}

// Register persists an IntentType. Re-registering the same name overwrites
// the previous definition; this is intentional so hosts can hot-update
// policy on restart without a migration.
func (r *Registry) Register(ctx context.Context, t domain.IntentType) error {
	if t.Name == "" {
		return &ErrValidation{Field: "name", Reason: "is required"}
	}
	if t.Policy.MaxIterations <= 0 {
		return &ErrValidation{Field: "policy.max_iterations", Reason: "must be > 0"}
	}
	if t.RegisteredAt.IsZero() {
		t.RegisteredAt = time.Now().UTC()
	}
	return r.repo.Register(ctx, t)
}

// Get returns the IntentType by name.
func (r *Registry) Get(ctx context.Context, name string) (domain.IntentType, error) {
	return r.repo.Get(ctx, name)
}

// List returns every registered IntentType.
func (r *Registry) List(ctx context.Context) ([]domain.IntentType, error) {
	return r.repo.List(ctx)
}

// Validate looks up the IntentType and validates the intent against the
// type's schema. The validator is intentionally minimal in Phase 1: it
// enforces required keys + scalar types declared in the JSON Schema's
// top-level "required" + "properties.<key>.type" fields. Full JSON-Schema
// support is a Phase-2 add-on once we adopt a schema library.
func (r *Registry) Validate(ctx context.Context, in domain.Intent) (domain.IntentType, error) {
	if in.Type == "" {
		return domain.IntentType{}, &ErrValidation{Field: "type", Reason: "is required"}
	}
	t, err := r.repo.Get(ctx, in.Type)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return domain.IntentType{}, fmt.Errorf("%q: %w", in.Type, ErrUnknownType)
		}
		return domain.IntentType{}, err
	}
	if err := validateAgainstSchema(in.Payload, t.Schema); err != nil {
		return domain.IntentType{}, err
	}
	return t, nil
}

// PreparePolicy applies the IntentType policy onto a Goal. Caller-supplied
// fields win; missing fields are filled from the policy defaults.
func PreparePolicy(t domain.IntentType, g domain.Goal) domain.Goal {
	if g.MaxIterations <= 0 {
		g.MaxIterations = t.Policy.MaxIterations
	}
	if g.Deadline == nil && t.Policy.DefaultDeadline > 0 {
		d := time.Now().UTC().Add(t.Policy.DefaultDeadline)
		g.Deadline = &d
	}
	return g
}

// validateAgainstSchema is a Phase-1 minimal subset of JSON Schema: required
// keys + scalar `type` checks on top-level properties. Adequate for the
// built-in types and for hosts whose intents have flat payloads.
func validateAgainstSchema(payload map[string]any, schema map[string]any) error {
	if len(schema) == 0 {
		return nil
	}
	if reqRaw, ok := schema["required"]; ok {
		req, ok := reqRaw.([]any)
		if ok {
			for _, k := range req {
				key, _ := k.(string)
				if key == "" {
					continue
				}
				if _, present := payload[key]; !present {
					return &ErrValidation{Field: key, Reason: "is required"}
				}
			}
		}
	}
	if propsRaw, ok := schema["properties"]; ok {
		props, _ := propsRaw.(map[string]any)
		for key, propAny := range props {
			prop, _ := propAny.(map[string]any)
			val, present := payload[key]
			if !present {
				continue
			}
			typeName, _ := prop["type"].(string)
			if typeName == "" {
				continue
			}
			if err := checkType(key, typeName, val); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkType(key, want string, v any) error {
	switch want {
	case "string":
		if _, ok := v.(string); !ok {
			return &ErrValidation{Field: key, Reason: "must be string"}
		}
	case "number":
		switch v.(type) {
		case float32, float64, int, int32, int64:
			return nil
		default:
			return &ErrValidation{Field: key, Reason: "must be number"}
		}
	case "integer":
		switch f := v.(type) {
		case int, int32, int64:
			return nil
		case float64:
			if f != float64(int64(f)) {
				return &ErrValidation{Field: key, Reason: "must be integer"}
			}
		default:
			return &ErrValidation{Field: key, Reason: "must be integer"}
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return &ErrValidation{Field: key, Reason: "must be boolean"}
		}
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return &ErrValidation{Field: key, Reason: "must be object"}
		}
	case "array":
		if _, ok := v.([]any); !ok {
			return &ErrValidation{Field: key, Reason: "must be array"}
		}
	default:
		// unknown type names are accepted in Phase 1
	}
	return nil
}

// Builtins returns the IntentTypes shipped with the runtime: `explain` (a
// read-only loop that never invokes Praxis) and `remediate` (a full loop
// gated by approval). Hosts call RegisterBuiltins during startup.
func Builtins() []domain.IntentType {
	return []domain.IntentType{
		{
			Name:        "explain",
			Description: "Read-only loop. Surfaces memories + signals + decisions but does not execute actions.",
			Schema: map[string]any{
				"type":     "object",
				"required": []any{"subject"},
				"properties": map[string]any{
					"subject": map[string]any{"type": "string"},
				},
			},
			Policy: domain.IntentPolicy{
				MaxIterations:   1,
				DefaultDeadline: 30 * time.Second,
				ReadOnly:        true,
			},
			RegisteredAt: time.Now().UTC(),
		},
		{
			Name:        "remediate",
			Description: "Full loop. Plans + executes corrective actions; gated by approval.",
			Schema: map[string]any{
				"type":     "object",
				"required": []any{"subject"},
				"properties": map[string]any{
					"subject": map[string]any{"type": "string"},
				},
			},
			Policy: domain.IntentPolicy{
				MaxIterations:   3,
				DefaultDeadline: 5 * time.Minute,
				RequireApproval: true,
			},
			RegisteredAt: time.Now().UTC(),
		},
	}
}

// RegisterBuiltins is a convenience used by `cmd/olymp` and tests. Returns
// the names of registered types so callers can emit a startup log line.
func (r *Registry) RegisterBuiltins(ctx context.Context) ([]string, error) {
	var names []string
	for _, t := range Builtins() {
		if err := r.Register(ctx, t); err != nil {
			return names, fmt.Errorf("register builtin %q: %w", t.Name, err)
		}
		names = append(names, t.Name)
	}
	return names, nil
}

// SubjectOf is a small helper for callers that want a normalised subject
// string. It falls back to the intent.Subject field when the payload does
// not carry one.
func SubjectOf(in domain.Intent) string {
	if in.Subject != "" {
		return in.Subject
	}
	if v, ok := in.Payload["subject"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
