// Package compliance produces audit exports + dashboards for governance.
//
// Provides:
//   - Export(): full audit trail per tenant + retention windowing
//   - Redact():  apply field-level redaction policies before export
//   - Dashboard(): rolled-up counters suited to ops review screens
//
// Export shape is a flat NDJSON stream so consumers can grep, pipe to bq,
// or load into a SIEM without bespoke parsers.
package compliance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// ExportRecord is one NDJSON line.
type ExportRecord struct {
	Kind       string         `json:"kind"` // "run" | "audit_event" | "provenance_step"
	Tenant     domain.Tenant  `json:"tenant,omitempty"`
	RunID      string         `json:"run_id,omitempty"`
	OccurredAt time.Time      `json:"occurred_at,omitempty"`
	Payload    map[string]any `json:"payload"`
}

// ExportConfig narrows the export window.
type ExportConfig struct {
	Tenant   domain.Tenant
	Since    time.Time
	Until    time.Time
	Redactor Redactor
}

// Redactor strips fields from a payload before it is written to the export
// stream. The default no-op redactor passes everything through.
type Redactor func(kind string, payload map[string]any) map[string]any

// NoOpRedactor leaves payloads untouched.
func NoOpRedactor(_ string, p map[string]any) map[string]any { return p }

// PIIRedactor blanks common PII keys (email, ip, phone) wherever they appear.
func PIIRedactor(_ string, p map[string]any) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		lk := strings.ToLower(k)
		if lk == "email" || lk == "ip" || lk == "phone" || strings.Contains(lk, "ssn") {
			out[k] = "[redacted]"
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			out[k] = PIIRedactor("", nested)
			continue
		}
		out[k] = v
	}
	return out
}

// Export streams every run + audit_event + provenance_step matching cfg as
// NDJSON to w. Records are emitted ordered by occurred_at ascending so a
// reviewer can read the timeline top-to-bottom.
func Export(ctx context.Context, w io.Writer, runs ports.RunRepo, audit ports.AuditRepo, cfg ExportConfig) error {
	if cfg.Redactor == nil {
		cfg.Redactor = NoOpRedactor
	}
	enc := json.NewEncoder(w)

	runList, err := runs.List(ctx, domain.RunFilter{Tenant: cfg.Tenant})
	if err != nil {
		return fmt.Errorf("compliance: list runs: %w", err)
	}

	// Write a "run" record + each provenance step + each audit event in
	// occurred_at order. We collect first, sort, then emit so the consumer
	// sees a strict timeline.
	type sortable struct {
		ts  time.Time
		rec ExportRecord
	}
	var rows []sortable

	for _, run := range runList {
		if !inWindow(run.StartedAt, cfg.Since, cfg.Until) {
			continue
		}
		runPayload := map[string]any{
			"id":      run.ID,
			"intent":  run.Intent,
			"caller":  run.Caller,
			"status":  string(run.Status),
			"goal":    run.Goal,
			"started": run.StartedAt,
		}
		rows = append(rows, sortable{run.StartedAt, ExportRecord{
			Kind: "run", Tenant: run.Tenant, RunID: run.ID,
			OccurredAt: run.StartedAt,
			Payload:    cfg.Redactor("run", runPayload),
		}})
		for _, step := range run.Provenance.Steps {
			if !inWindow(step.StartedAt, cfg.Since, cfg.Until) {
				continue
			}
			stepPayload := map[string]any{
				"iteration":  step.Iteration,
				"stage":      string(step.Stage),
				"layer":      step.LayerRef.Layer,
				"layer_ref":  step.LayerRef.ID,
				"inputs":     step.Inputs,
				"outputs":    step.Outputs,
				"started_at": step.StartedAt,
				"completed":  step.CompletedAt,
			}
			rows = append(rows, sortable{step.StartedAt, ExportRecord{
				Kind: "provenance_step", Tenant: run.Tenant, RunID: run.ID,
				OccurredAt: step.StartedAt,
				Payload:    cfg.Redactor("provenance_step", stepPayload),
			}})
		}
		events, err := audit.ListForRun(ctx, run.ID)
		if err != nil {
			return fmt.Errorf("compliance: audit %s: %w", run.ID, err)
		}
		for _, ev := range events {
			if !inWindow(ev.CreatedAt, cfg.Since, cfg.Until) {
				continue
			}
			rows = append(rows, sortable{ev.CreatedAt, ExportRecord{
				Kind: "audit_event", Tenant: run.Tenant, RunID: run.ID,
				OccurredAt: ev.CreatedAt,
				Payload:    cfg.Redactor("audit_event", map[string]any{"id": ev.ID, "kind": ev.Kind, "detail": ev.Detail}),
			}})
		}
	}

	// stable sort by ts
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].ts.Before(rows[j-1].ts); j-- {
			rows[j-1], rows[j] = rows[j], rows[j-1]
		}
	}
	for _, row := range rows {
		if err := enc.Encode(row.rec); err != nil {
			return fmt.Errorf("compliance: encode: %w", err)
		}
	}
	return nil
}

// Dashboard returns rolled-up counters for one tenant + window.
type Dashboard struct {
	Tenant            domain.Tenant `json:"tenant"`
	Since             time.Time     `json:"since"`
	Until             time.Time     `json:"until"`
	TotalRuns         int           `json:"total_runs"`
	Completed         int           `json:"completed"`
	Failed            int           `json:"failed"`
	Cancelled         int           `json:"cancelled"`
	InFlight          int           `json:"in_flight"`
	ApprovalsRequired int           `json:"approvals_required"`
	OutcomesWritten   int           `json:"outcomes_written"`
}

// BuildDashboard rolls up the runs + audit events for the configured tenant.
func BuildDashboard(ctx context.Context, runs ports.RunRepo, audit ports.AuditRepo, cfg ExportConfig) (Dashboard, error) {
	out := Dashboard{Tenant: cfg.Tenant, Since: cfg.Since, Until: cfg.Until}
	runList, err := runs.List(ctx, domain.RunFilter{Tenant: cfg.Tenant})
	if err != nil {
		return out, err
	}
	for _, run := range runList {
		if !inWindow(run.StartedAt, cfg.Since, cfg.Until) {
			continue
		}
		out.TotalRuns++
		switch run.Status {
		case domain.StatusCompleted:
			out.Completed++
		case domain.StatusFailed:
			out.Failed++
		case domain.StatusCancelled:
			out.Cancelled++
		default:
			out.InFlight++
		}
		events, _ := audit.ListForRun(ctx, run.ID)
		for _, ev := range events {
			if ev.Kind == "approval_required" {
				out.ApprovalsRequired++
			}
			if ev.Kind == "outcome_written" {
				out.OutcomesWritten++
			}
		}
	}
	return out, nil
}

func inWindow(t, since, until time.Time) bool {
	if !since.IsZero() && t.Before(since) {
		return false
	}
	if !until.IsZero() && t.After(until) {
		return false
	}
	return true
}
