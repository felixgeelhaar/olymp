package compliance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/audit"
	"github.com/felixgeelhaar/olymp/internal/compliance"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
	"github.com/felixgeelhaar/olymp/internal/store/memory"
)

func seed(t *testing.T) (ports.Repos, domain.Tenant) {
	t.Helper()
	repos := memory.New()
	tenant := domain.Tenant{Org: "acme", Team: "platform"}
	now := time.Now().UTC()
	r := domain.Run{
		ID: "r-1", Tenant: tenant,
		Intent:  domain.Intent{Type: "remediate", Subject: "x"},
		Session: domain.SessionRef{ID: "s"}, Caller: domain.CallerRef{Type: "user", ID: "u-1"},
		Status: domain.StatusCompleted, Goal: domain.Goal{MaxIterations: 1},
		StartedAt: now.Add(-10 * time.Minute), UpdatedAt: now, CompletedAt: &now,
	}
	if err := repos.Runs.Save(context.Background(), r); err != nil {
		t.Fatalf("save: %v", err)
	}
	_ = repos.Runs.AppendProvenance(context.Background(), "r-1", domain.ProvenanceStep{
		Iteration: 0, Stage: domain.StatusObserving,
		StartedAt: now.Add(-9 * time.Minute), CompletedAt: now.Add(-8 * time.Minute),
		LayerRef: domain.LayerRef{Layer: "mnemos", ID: "m-1"},
		Inputs:   map[string]any{"email": "alice@example.com"},
	})
	logger := audit.New(repos.Audit, nil)
	_ = logger.Submitted(context.Background(), r)
	_ = logger.OutcomeWritten(context.Background(), "r-1", domain.OutcomeEvent{
		RunID: "r-1", ActionID: "a-1", Status: "succeeded",
	})
	return repos, tenant
}

func TestExport_OrdersByOccurredAt(t *testing.T) {
	repos, tenant := seed(t)
	var buf bytes.Buffer
	err := compliance.Export(context.Background(), &buf, repos.Runs, repos.Audit, compliance.ExportConfig{
		Tenant: tenant,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("got %d lines, want >=3", len(lines))
	}
	var prev time.Time
	for _, line := range lines {
		var rec compliance.ExportRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !prev.IsZero() && rec.OccurredAt.Before(prev) {
			t.Errorf("out-of-order: %v before %v", rec.OccurredAt, prev)
		}
		prev = rec.OccurredAt
	}
}

func TestExport_PIIRedactor(t *testing.T) {
	repos, tenant := seed(t)
	var buf bytes.Buffer
	err := compliance.Export(context.Background(), &buf, repos.Runs, repos.Audit, compliance.ExportConfig{
		Tenant:   tenant,
		Redactor: compliance.PIIRedactor,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "alice@example.com") {
		t.Fatalf("PII leaked: %s", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("redactor did not run; out=%s", out)
	}
}

func TestExport_RespectsWindow(t *testing.T) {
	repos, tenant := seed(t)
	var buf bytes.Buffer
	future := time.Now().UTC().Add(time.Hour)
	err := compliance.Export(context.Background(), &buf, repos.Runs, repos.Audit, compliance.ExportConfig{
		Tenant: tenant,
		Since:  future,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("future window returned data: %s", buf.String())
	}
}

func TestBuildDashboard(t *testing.T) {
	repos, tenant := seed(t)
	dash, err := compliance.BuildDashboard(context.Background(), repos.Runs, repos.Audit, compliance.ExportConfig{
		Tenant: tenant,
	})
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	if dash.TotalRuns != 1 || dash.Completed != 1 {
		t.Fatalf("dash=%+v", dash)
	}
	if dash.OutcomesWritten != 1 {
		t.Fatalf("outcomes=%d want 1", dash.OutcomesWritten)
	}
}
