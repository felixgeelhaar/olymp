package adapters_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/adapters/chronos"
	"github.com/felixgeelhaar/olymp/internal/adapters/httpx"
	"github.com/felixgeelhaar/olymp/internal/adapters/mnemos"
	"github.com/felixgeelhaar/olymp/internal/adapters/nous"
	"github.com/felixgeelhaar/olymp/internal/adapters/praxis"
	"github.com/felixgeelhaar/olymp/internal/domain"
)

func TestMnemosClient_Recall_Append_Get(t *testing.T) {
	wantMem := domain.MemoryRef{ID: "m-1", Kind: "claim", Confidence: 0.9}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/memories/recall":
			_ = json.NewEncoder(w).Encode(map[string]any{"memories": []domain.MemoryRef{wantMem}})
		case "/v1/events":
			w.WriteHeader(204)
		case "/v1/memories/m-1":
			_ = json.NewEncoder(w).Encode(wantMem)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := mnemos.New(httpx.Config{BaseURL: srv.URL, InitialDelay: time.Millisecond})
	mems, err := c.Recall(context.Background(), domain.MemoryQuery{RunID: "r-1"})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(mems) != 1 || mems[0].ID != "m-1" {
		t.Fatalf("recall got=%+v", mems)
	}
	if err := c.Append(context.Background(), domain.OutcomeEvent{Type: "olymp.outcome", RunID: "r-1"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := c.Get(context.Background(), "m-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "m-1" {
		t.Fatalf("get id=%s", got.ID)
	}
}

func TestChronosClient_Signals_Get(t *testing.T) {
	wantSig := domain.SignalRef{ID: "s-1", Pattern: "spike", Strength: 0.9, Confidence: 0.8}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/signals/query":
			_ = json.NewEncoder(w).Encode(map[string]any{"signals": []domain.SignalRef{wantSig}})
		case "/v1/signals/s-1":
			_ = json.NewEncoder(w).Encode(wantSig)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := chronos.New(httpx.Config{BaseURL: srv.URL, InitialDelay: time.Millisecond})
	sigs, err := c.Signals(context.Background(), domain.SignalQuery{RunID: "r-1"})
	if err != nil {
		t.Fatalf("signals: %v", err)
	}
	if len(sigs) != 1 || sigs[0].Pattern != "spike" {
		t.Fatalf("signals got=%+v", sigs)
	}
	got, err := c.Get(context.Background(), "s-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "s-1" {
		t.Fatalf("get id=%s", got.ID)
	}
}

func TestNousClient_Decide_Get(t *testing.T) {
	wantDec := domain.DecisionRef{ID: "d-1", Rationale: "looks like a spike"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/decisions":
			_ = json.NewEncoder(w).Encode(wantDec)
		case "/v1/decisions/d-1":
			_ = json.NewEncoder(w).Encode(wantDec)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := nous.New(httpx.Config{BaseURL: srv.URL, InitialDelay: time.Millisecond})
	dec, err := c.Decide(context.Background(), domain.DecisionRequest{RunID: "r-1"})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if dec.ID != "d-1" {
		t.Fatalf("decide id=%s", dec.ID)
	}
	got, err := c.Get(context.Background(), "d-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "d-1" {
		t.Fatalf("get id=%s", got.ID)
	}
}

func TestPraxisClient_List_Execute_DryRun(t *testing.T) {
	wantCap := domain.CapabilityRef{Name: "send_message", Idempotent: true, Simulatable: true}
	wantRes := domain.ActionResult{ActionID: "a-1", Status: "succeeded", ExternalID: "vendor-handle"}
	wantSim := domain.SimulationRef{ActionID: "a-1", Reversible: true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": []domain.CapabilityRef{wantCap}})
		case "/v1/actions":
			_ = json.NewEncoder(w).Encode(wantRes)
		case "/v1/actions/dry-run":
			_ = json.NewEncoder(w).Encode(wantSim)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := praxis.New(httpx.Config{BaseURL: srv.URL, InitialDelay: time.Millisecond})
	caps, err := c.ListCapabilities(context.Background())
	if err != nil {
		t.Fatalf("caps: %v", err)
	}
	if len(caps) != 1 || caps[0].Name != "send_message" {
		t.Fatalf("caps=%+v", caps)
	}
	res, err := c.Execute(context.Background(), domain.ActionRequest{ID: "a-1", Capability: "send_message"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Status != "succeeded" {
		t.Fatalf("res=%+v", res)
	}
	sim, err := c.DryRun(context.Background(), domain.ActionRequest{ID: "a-1", Capability: "send_message"})
	if err != nil {
		t.Fatalf("dryrun: %v", err)
	}
	if !sim.Reversible {
		t.Fatalf("sim=%+v", sim)
	}
}
