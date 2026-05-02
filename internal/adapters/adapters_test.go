// Package adapters_test is the cross-adapter smoke test: each cognitive
// adapter is wired against an httptest fake of the real service's HTTP
// surface and exercised through its public verbs. The shapes here must
// stay in sync with the real services' DTOs.
package adapters_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/felixgeelhaar/olymp/internal/adapters/chronos"
	"github.com/felixgeelhaar/olymp/internal/adapters/httpx"
	"github.com/felixgeelhaar/olymp/internal/adapters/mnemos"
	"github.com/felixgeelhaar/olymp/internal/adapters/nous"
	"github.com/felixgeelhaar/olymp/internal/adapters/praxis"
	"github.com/felixgeelhaar/olymp/internal/domain"
)

func TestMnemosClient_Recall_Append_Get(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/claims":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"claims": []map[string]any{{"id": "c-1", "type": "fact", "confidence": 0.9, "status": "active"}},
				"total":  1,
			})
		case "/v1/events":
			_ = json.NewEncoder(w).Encode(map[string]any{"accepted": 1, "skipped": 0})
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
	if len(mems) != 1 || mems[0].ID != "c-1" {
		t.Fatalf("recall got=%+v", mems)
	}
	if err := c.Append(context.Background(), domain.OutcomeEvent{Type: "olymp.outcome", RunID: "r-1"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := c.Get(context.Background(), "c-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "c-1" {
		t.Fatalf("get id=%s", got.ID)
	}
}

func TestChronosClient_Signals_Get(t *testing.T) {
	scopeID := uuid.NewString()
	signalID := uuid.NewString()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/signals":
			if r.URL.Query().Get("scope_id") == "" {
				http.Error(w, "scope_id required", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"signals": []map[string]any{{"id": signalID, "pattern": "spike", "strength": 0.9, "confidence": 0.8}},
				"count":   1,
			})
		case "/v1/signals/" + signalID:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": signalID, "pattern": "spike", "strength": 0.9, "confidence": 0.8,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := chronos.NewWithConfig(chronos.Config{
		HTTP:           httpx.Config{BaseURL: srv.URL, InitialDelay: time.Millisecond},
		DefaultScopeID: scopeID,
	})
	sigs, err := c.Signals(context.Background(), domain.SignalQuery{RunID: "r-1"})
	if err != nil {
		t.Fatalf("signals: %v", err)
	}
	if len(sigs) != 1 || sigs[0].Pattern != "spike" {
		t.Fatalf("signals got=%+v", sigs)
	}
	got, err := c.Get(context.Background(), signalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != signalID {
		t.Fatalf("get id=%s", got.ID)
	}
}

func TestChronosClient_Signals_EmptyWhenNoScope(t *testing.T) {
	c := chronos.NewWithConfig(chronos.Config{
		HTTP: httpx.Config{BaseURL: "http://unused", InitialDelay: time.Millisecond},
	})
	sigs, err := c.Signals(context.Background(), domain.SignalQuery{RunID: "r-1"})
	if err != nil {
		t.Fatalf("signals: %v", err)
	}
	if len(sigs) != 0 {
		t.Fatalf("want empty, got %+v", sigs)
	}
}

func TestNousClient_Decide_Get(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/extract":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"considered":  3,
				"saved_ids":   []string{"c-1", "c-2"},
				"dropped":     1,
				"decision_id": "d-1",
			})
		case "/v1/decisions/d-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "d-1"})
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

func TestNousClient_Decide_SyntheticIDWhenNoneReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"considered":  0,
			"saved_ids":   []string{},
			"dropped":     0,
			"decision_id": "",
		})
	}))
	defer srv.Close()

	c := nous.New(httpx.Config{BaseURL: srv.URL, InitialDelay: time.Millisecond})
	dec, err := c.Decide(context.Background(), domain.DecisionRequest{RunID: "r-42"})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if dec.ID == "" {
		t.Fatal("expected synthetic id")
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
		case "/v1/actions/a-1/dry-run":
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
