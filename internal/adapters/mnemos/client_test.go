package mnemos_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/adapters/httpx"
	"github.com/felixgeelhaar/olymp/internal/adapters/mnemos"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// fakeMnemos models the slice of Mnemos's HTTP API the adapter actually
// touches — claims listing + events append. Each handler asserts on the
// request shape and records what was received.
type fakeMnemos struct {
	t              *testing.T
	claims         []map[string]any
	gotStatus      string
	gotLimit       string
	gotEventsBody  []byte
	statusOnEvents int
}

func (f *fakeMnemos) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/claims", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		f.gotStatus = r.URL.Query().Get("status")
		f.gotLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"claims": f.claims,
			"total":  len(f.claims),
			"limit":  10,
			"offset": 0,
		})
	})
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		f.gotEventsBody = body
		if f.statusOnEvents != 0 {
			w.WriteHeader(f.statusOnEvents)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted": 1, "skipped": 0})
	})
	return mux
}

func TestRecall_MapsClaimsToMemoryRefs(t *testing.T) {
	t.Parallel()
	fake := &fakeMnemos{
		t: t,
		claims: []map[string]any{
			{"id": "c1", "text": "payments are slow", "type": "fact", "confidence": 0.92, "status": "active", "created_at": "2026-05-01T00:00:00Z"},
			{"id": "c2", "text": "checkout uses redis", "type": "fact", "confidence": 0.71, "status": "active", "created_at": "2026-05-02T00:00:00Z"},
		},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	c := mnemos.New(httpx.Config{BaseURL: srv.URL})
	got, err := c.Recall(context.Background(), domain.MemoryQuery{Limit: 10})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "c1" || got[0].Kind != "fact" || got[0].Confidence != 0.92 {
		t.Errorf("got[0] = %+v", got[0])
	}
	if fake.gotStatus != "active" {
		t.Errorf("status filter = %q, want active", fake.gotStatus)
	}
	if fake.gotLimit != "10" {
		t.Errorf("limit = %q, want 10", fake.gotLimit)
	}
}

func TestRecall_DefaultsLimitWhenZero(t *testing.T) {
	t.Parallel()
	fake := &fakeMnemos{t: t}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	c := mnemos.New(httpx.Config{BaseURL: srv.URL})
	if _, err := c.Recall(context.Background(), domain.MemoryQuery{}); err != nil {
		t.Fatalf("recall: %v", err)
	}
	if fake.gotLimit == "" || fake.gotLimit == "0" {
		t.Errorf("expected non-zero default limit, got %q", fake.gotLimit)
	}
}

func TestAppend_PostsBatchedEventBody(t *testing.T) {
	t.Parallel()
	fake := &fakeMnemos{t: t}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	c := mnemos.New(httpx.Config{BaseURL: srv.URL})
	err := c.Append(context.Background(), domain.OutcomeEvent{
		Type:       "olymp.outcome",
		RunID:      "run-1",
		Iteration:  3,
		Intent:     domain.Intent{Type: "remediate"},
		ActionID:   "act-7",
		Capability: "deploy",
		Status:     "succeeded",
		Timestamp:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	var body struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(fake.gotEventsBody, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(body.Events))
	}
	ev := body.Events[0]
	if ev["run_id"] != "run-1" {
		t.Errorf("run_id = %v", ev["run_id"])
	}
	md, ok := ev["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata not an object: %v", ev["metadata"])
	}
	if md["capability"] != "deploy" || md["status"] != "succeeded" {
		t.Errorf("metadata = %+v", md)
	}
	idStr, _ := ev["id"].(string)
	if !strings.HasPrefix(idStr, "olymp:run-1:3:act-7") {
		t.Errorf("event id = %q, want deterministic prefix", idStr)
	}
}

func TestAppend_RejectsMissingRunID(t *testing.T) {
	t.Parallel()
	c := mnemos.New(httpx.Config{BaseURL: "http://unused"})
	err := c.Append(context.Background(), domain.OutcomeEvent{Type: "olymp.outcome"})
	if err == nil {
		t.Fatal("want error on missing run_id")
	}
}

func TestAppend_PropagatesUpstreamError(t *testing.T) {
	t.Parallel()
	fake := &fakeMnemos{t: t, statusOnEvents: http.StatusBadGateway}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	c := mnemos.New(httpx.Config{BaseURL: srv.URL, MaxAttempts: 1})
	err := c.Append(context.Background(), domain.OutcomeEvent{
		Type:      "olymp.outcome",
		RunID:     "run-1",
		Timestamp: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("want error")
	}
}

func TestGet_ReturnsClaimWhenIDMatches(t *testing.T) {
	t.Parallel()
	fake := &fakeMnemos{t: t, claims: []map[string]any{
		{"id": "c1", "text": "x", "type": "fact", "confidence": 0.5, "status": "active"},
		{"id": "c2", "text": "y", "type": "hypothesis", "confidence": 0.4, "status": "active"},
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	c := mnemos.New(httpx.Config{BaseURL: srv.URL})
	got, err := c.Get(context.Background(), "c2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "c2" || got.Kind != "hypothesis" {
		t.Errorf("got = %+v", got)
	}
}

func TestGet_ReturnsErrNotFoundOnMiss(t *testing.T) {
	t.Parallel()
	fake := &fakeMnemos{t: t}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	c := mnemos.New(httpx.Config{BaseURL: srv.URL})
	_, err := c.Get(context.Background(), "missing")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestGet_RejectsEmptyID(t *testing.T) {
	t.Parallel()
	c := mnemos.New(httpx.Config{BaseURL: "http://unused"})
	if _, err := c.Get(context.Background(), ""); err == nil {
		t.Fatal("want error on empty id")
	}
}
