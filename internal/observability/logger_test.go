package observability_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/observability"
)

func TestLogStep_EmitsCanonicalFields(t *testing.T) {
	var buf bytes.Buffer
	log := observability.NewJSON(&buf)
	observability.LogStep(log, "stage_completed", observability.LoopFields{
		RunID: "r-1", Iteration: 2, Stage: domain.StatusObserving,
		Layer: "mnemos", IntentType: "explain",
		CallerType: "agent", CallerID: "a-1",
		DurationMs: 42,
	})
	out := buf.String()
	for _, want := range []string{`"run_id":"r-1"`, `"iteration":2`, `"stage":"observing"`,
		`"layer":"mnemos"`, `"intent_type":"explain"`, `"caller_type":"agent"`,
		`"caller_id":"a-1"`, `"duration_ms":42`, `"stage_completed"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %s\nout=%s", want, out)
		}
	}
}

func TestLogStep_NilLoggerNoOp(t *testing.T) {
	observability.LogStep(nil, "x", observability.LoopFields{})
	observability.LogError(nil, "x", "r-1", errors.New("e"), nil)
	// no panic = pass
}

func TestHealthRegistry_TracksLayers(t *testing.T) {
	reg := observability.NewHealthRegistry()
	if got := reg.Snapshot().Status; got != "ok" {
		t.Fatalf("initial status=%s want ok", got)
	}
	reg.MarkError("mnemos", time.Now(), errors.New("down"))
	snap := reg.Snapshot()
	if snap.Status != "degraded" {
		t.Fatalf("status=%s want degraded", snap.Status)
	}
	var mnemos *observability.LayerStatus
	for i := range snap.Layers {
		if snap.Layers[i].Layer == "mnemos" {
			mnemos = &snap.Layers[i]
		}
	}
	if mnemos == nil || mnemos.Healthy || mnemos.LastError != "down" {
		t.Fatalf("mnemos status=%+v", mnemos)
	}
	reg.MarkSuccess("mnemos", time.Now())
	if reg.Snapshot().Status != "ok" {
		t.Fatal("recovery did not flip back to ok")
	}
}

func TestHealthRegistry_UnknownLayer(t *testing.T) {
	reg := observability.NewHealthRegistry()
	reg.MarkSuccess("custom", time.Now())
	found := false
	for _, s := range reg.Snapshot().Layers {
		if s.Layer == "custom" {
			found = true
		}
	}
	if !found {
		t.Fatal("custom layer not registered on first MarkSuccess")
	}
}
