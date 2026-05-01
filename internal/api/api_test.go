package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/adapters/fake"
	"github.com/felixgeelhaar/olymp/internal/api"
	"github.com/felixgeelhaar/olymp/internal/audit"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/engine"
	"github.com/felixgeelhaar/olymp/internal/eventbus"
	"github.com/felixgeelhaar/olymp/internal/intent"
	"github.com/felixgeelhaar/olymp/internal/observability"
	"github.com/felixgeelhaar/olymp/internal/ports"
	"github.com/felixgeelhaar/olymp/internal/store/memory"
)

type harness struct {
	svc      *api.Service
	bus      *eventbus.Bus
	repos    ports.Repos
	registry *intent.Registry
	mnemos   *fake.Mnemos
	nous     *fake.Nous
	praxis   *fake.Praxis
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	repos := memory.New()
	registry := intent.New(repos.IntentTypes)
	if _, err := registry.RegisterBuiltins(context.Background()); err != nil {
		t.Fatalf("builtins: %v", err)
	}
	auditor := audit.New(repos.Audit, nil)
	bus := eventbus.New(64)
	mnemos := &fake.Mnemos{Memories: []domain.MemoryRef{{ID: "m-1"}}}
	chronos := &fake.Chronos{}
	nous := &fake.Nous{ScriptedDecision: domain.DecisionRef{ID: "d-1"}}
	praxis := &fake.Praxis{Result: domain.ActionResult{Status: "succeeded"}}
	eng := engine.New(engine.Config{}, ports.Layers{
		Mnemos: mnemos, Chronos: chronos, Nous: nous, Praxis: praxis,
	}, repos, registry, auditor, bus)
	svc := api.NewService(repos, registry, auditor, bus, eng)
	return &harness{svc: svc, bus: bus, repos: repos, registry: registry, mnemos: mnemos, nous: nous, praxis: praxis}
}

func TestService_SubmitInspectRoundtrip(t *testing.T) {
	h := newHarness(t)
	ctx := api.WithCaller(context.Background(), domain.CallerRef{Type: "user", ID: "u-1"})
	run, err := h.svc.Submit(ctx, domain.Intent{
		Type:    "explain",
		Subject: "x",
		Payload: map[string]any{"subject": "x"},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if run.Status != domain.StatusCompleted {
		t.Fatalf("status=%s want completed", run.Status)
	}
	snap, err := h.svc.Inspect(ctx, run.ID)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if snap.Run.ID != run.ID {
		t.Fatalf("snap=%+v", snap)
	}
}

func TestService_Submit_RejectsUnknownIntent(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.Submit(context.Background(), domain.Intent{Type: "nope"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, intent.ErrUnknownType) {
		t.Fatalf("err=%v want ErrUnknownType", err)
	}
}

func TestService_Steer(t *testing.T) {
	h := newHarness(t)
	// stage a paused run
	run := domain.Run{
		ID: "r-1", Intent: domain.Intent{Type: "remediate"},
		Session:   domain.SessionRef{ID: "s"},
		Caller:    domain.CallerRef{Type: "user", ID: "u"},
		Status:    domain.StatusPaused,
		Goal:      domain.Goal{MaxIterations: 1},
		StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := h.repos.Runs.Save(context.Background(), run); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := h.svc.Steer(context.Background(), "r-1", domain.SteerCommand{Kind: "cancel"}); err != nil {
		t.Fatalf("steer: %v", err)
	}
	got, _ := h.repos.Runs.Get(context.Background(), "r-1")
	if got.Status != domain.StatusCancelled {
		t.Fatalf("status=%s want cancelled", got.Status)
	}
}

func TestService_Steer_Unknown(t *testing.T) {
	h := newHarness(t)
	run := domain.Run{
		ID: "r-1", Intent: domain.Intent{Type: "remediate"},
		Session: domain.SessionRef{ID: "s"}, Caller: domain.CallerRef{Type: "user", ID: "u"},
		Status: domain.StatusObserving, Goal: domain.Goal{MaxIterations: 1},
		StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	_ = h.repos.Runs.Save(context.Background(), run)
	if err := h.svc.Steer(context.Background(), "r-1", domain.SteerCommand{Kind: "bogus"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestService_Stream(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := h.svc.Stream(ctx, domain.RunFilter{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	h.bus.Publish(domain.RunEvent{RunID: "r-1", Kind: "transitioned"})
	select {
	case ev := <-ch:
		if ev.RunID != "r-1" {
			t.Fatalf("ev=%+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
}

func TestHTTP_SubmitInspectSteer(t *testing.T) {
	h := newHarness(t)
	srv := httptest.NewServer(api.HTTPHandler(h.svc, h.registry, observability.NewHealthRegistry()))
	defer srv.Close()

	body, _ := json.Marshal(api.SubmitRequest{
		Type: "explain", Subject: "x", Payload: map[string]any{"subject": "x"},
	})
	resp, err := http.Post(srv.URL+"/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var run domain.Run
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if run.ID == "" {
		t.Fatal("empty run id")
	}

	resp2, err := http.Get(srv.URL + "/v1/runs/" + run.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("inspect status=%d", resp2.StatusCode)
	}
	resp2.Body.Close()

	steerBody, _ := json.Marshal(api.SteerRequest{Kind: "cancel"})
	resp3, err := http.Post(srv.URL+"/v1/runs/"+run.ID+"/steer", "application/json", bytes.NewReader(steerBody))
	if err != nil {
		t.Fatalf("steer: %v", err)
	}
	// already terminal — service rejects with 500 (steer terminal run); accept either.
	if resp3.StatusCode != http.StatusNoContent && resp3.StatusCode != http.StatusInternalServerError {
		t.Fatalf("steer status=%d", resp3.StatusCode)
	}
	resp3.Body.Close()
}

func TestHTTP_NotFound(t *testing.T) {
	h := newHarness(t)
	srv := httptest.NewServer(api.HTTPHandler(h.svc, h.registry, nil))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/runs/does-not-exist")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHTTP_HealthzExposesRegistry(t *testing.T) {
	h := newHarness(t)
	reg := observability.NewHealthRegistry()
	srv := httptest.NewServer(api.HTTPHandler(h.svc, h.registry, reg))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("body=%s", body)
	}
}

func TestHTTP_BadBody(t *testing.T) {
	h := newHarness(t)
	srv := httptest.NewServer(api.HTTPHandler(h.svc, h.registry, nil))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/runs", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	resp.Body.Close()
}
