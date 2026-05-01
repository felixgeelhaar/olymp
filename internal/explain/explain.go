// Package explain reconstructs the full Memory → Signal → Decision → Action
// → Outcome chain for one Run, with citations and confidence, in a form
// suited to compliance reviews and post-mortems.
package explain

import (
	"context"
	"fmt"
	"strings"

	"github.com/felixgeelhaar/olymp/internal/audit"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// Chain is the explainability output for one Run.
type Chain struct {
	RunID      string             `json:"run_id"`
	Intent     domain.Intent      `json:"intent"`
	Caller     domain.CallerRef   `json:"caller"`
	Goal       domain.Goal        `json:"goal"`
	Iterations []IterationSummary `json:"iterations"`
	Status     domain.RunStatus   `json:"status"`
	LastError  *domain.RunError   `json:"last_error,omitempty"`
}

// IterationSummary is one cycle's worth of provenance, resolved against the
// underlying cognitive layers.
type IterationSummary struct {
	Iteration int              `json:"iteration"`
	Memories  []Citation       `json:"memories,omitempty"`
	Signals   []Citation       `json:"signals,omitempty"`
	Decision  *DecisionSummary `json:"decision,omitempty"`
	Actions   []ActionSummary  `json:"actions,omitempty"`
	Outcomes  []OutcomeSummary `json:"outcomes,omitempty"`
}

// Citation is a resolved cross-layer reference + confidence (when available).
type Citation struct {
	Layer      string  `json:"layer"`
	ID         string  `json:"id"`
	Kind       string  `json:"kind,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// DecisionSummary is the resolved decision step.
type DecisionSummary struct {
	ID               string `json:"id"`
	Rationale        string `json:"rationale,omitempty"`
	RequiresApproval bool   `json:"requires_approval"`
	ActionCount      int    `json:"action_count"`
}

// ActionSummary is one Praxis result.
type ActionSummary struct {
	ID         string `json:"id"`
	Capability string `json:"capability,omitempty"`
}

// OutcomeSummary is one writeback to Mnemos.
type OutcomeSummary struct {
	MemoryID string `json:"memory_id"`
}

// Build resolves the chain by combining the persisted Run with live lookups
// against Mnemos / Chronos / Nous (Praxis is read from provenance only;
// re-fetching action results would re-execute, which is forbidden).
func Build(ctx context.Context, runs ports.RunRepo, layers ports.Layers, runID string) (Chain, error) {
	chainSrc, err := audit.Reconstruct(ctx, runs, runID)
	if err != nil {
		return Chain{}, err
	}
	run, err := runs.Get(ctx, runID)
	if err != nil {
		return Chain{}, err
	}
	out := Chain{
		RunID:     run.ID,
		Intent:    run.Intent,
		Caller:    run.Caller,
		Goal:      run.Goal,
		Status:    run.Status,
		LastError: run.LastError,
	}
	byIter := groupSteps(chainSrc.Steps)
	for _, iter := range sortedKeys(byIter) {
		summary := IterationSummary{Iteration: iter}
		for _, step := range byIter[iter] {
			switch step.Stage {
			case domain.StatusObserving:
				if c := resolveMemory(ctx, layers, step.LayerRef.ID); c != nil {
					summary.Memories = append(summary.Memories, *c)
				}
			case domain.StatusUnderstanding:
				if c := resolveSignal(ctx, layers, step.LayerRef.ID); c != nil {
					summary.Signals = append(summary.Signals, *c)
				}
			case domain.StatusDeciding:
				if d := resolveDecision(ctx, layers, step.LayerRef.ID); d != nil {
					summary.Decision = d
				}
			case domain.StatusActing:
				if step.LayerRef.ID != "" {
					summary.Actions = append(summary.Actions, ActionSummary{ID: step.LayerRef.ID})
				}
			case domain.StatusLearning:
				if step.LayerRef.ID != "" {
					summary.Outcomes = append(summary.Outcomes, OutcomeSummary{MemoryID: step.LayerRef.ID})
				}
			}
		}
		out.Iterations = append(out.Iterations, summary)
	}
	return out, nil
}

// Markdown renders a Chain as a compliance-friendly Markdown document.
func (c Chain) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Run %s — %s\n\n", c.RunID, c.Intent.Type)
	fmt.Fprintf(&b, "- **Subject:** %s\n", c.Intent.Subject)
	fmt.Fprintf(&b, "- **Caller:** %s/%s\n", c.Caller.Type, c.Caller.ID)
	fmt.Fprintf(&b, "- **Status:** %s\n\n", c.Status)
	for _, it := range c.Iterations {
		fmt.Fprintf(&b, "## Iteration %d\n\n", it.Iteration)
		if len(it.Memories) > 0 {
			b.WriteString("**Memories:**\n")
			for _, m := range it.Memories {
				fmt.Fprintf(&b, "- %s/%s (confidence %.2f)\n", m.Layer, m.ID, m.Confidence)
			}
			b.WriteString("\n")
		}
		if len(it.Signals) > 0 {
			b.WriteString("**Signals:**\n")
			for _, s := range it.Signals {
				if s.Kind != "" {
					fmt.Fprintf(&b, "- %s/%s (%s, confidence %.2f)\n", s.Layer, s.ID, s.Kind, s.Confidence)
				} else {
					fmt.Fprintf(&b, "- %s/%s\n", s.Layer, s.ID)
				}
			}
			b.WriteString("\n")
		}
		if it.Decision != nil {
			fmt.Fprintf(&b, "**Decision:** %s — %s\n\n", it.Decision.ID, it.Decision.Rationale)
		}
		if len(it.Actions) > 0 {
			b.WriteString("**Actions:**\n")
			for _, a := range it.Actions {
				fmt.Fprintf(&b, "- %s\n", a.ID)
			}
			b.WriteString("\n")
		}
		if len(it.Outcomes) > 0 {
			b.WriteString("**Outcomes (Mnemos):**\n")
			for _, o := range it.Outcomes {
				fmt.Fprintf(&b, "- %s\n", o.MemoryID)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func groupSteps(steps []domain.ProvenanceStep) map[int][]domain.ProvenanceStep {
	out := map[int][]domain.ProvenanceStep{}
	for _, s := range steps {
		out[s.Iteration] = append(out[s.Iteration], s)
	}
	return out
}

func sortedKeys(m map[int][]domain.ProvenanceStep) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

func resolveMemory(ctx context.Context, l ports.Layers, id string) *Citation {
	if id == "" || l.Mnemos == nil {
		return nil
	}
	ref, err := l.Mnemos.Get(ctx, id)
	if err != nil {
		return &Citation{Layer: "mnemos", ID: id}
	}
	return &Citation{Layer: "mnemos", ID: ref.ID, Kind: ref.Kind, Confidence: ref.Confidence}
}

func resolveSignal(ctx context.Context, l ports.Layers, id string) *Citation {
	if id == "" || l.Chronos == nil {
		return nil
	}
	ref, err := l.Chronos.Get(ctx, id)
	if err != nil {
		return &Citation{Layer: "chronos", ID: id}
	}
	return &Citation{Layer: "chronos", ID: ref.ID, Kind: ref.Pattern, Confidence: ref.Confidence}
}

func resolveDecision(ctx context.Context, l ports.Layers, id string) *DecisionSummary {
	if id == "" || l.Nous == nil {
		return nil
	}
	ref, err := l.Nous.Get(ctx, id)
	if err != nil {
		return &DecisionSummary{ID: id}
	}
	return &DecisionSummary{
		ID: ref.ID, Rationale: ref.Rationale,
		RequiresApproval: ref.RequiresApproval,
		ActionCount:      len(ref.Actions),
	}
}
