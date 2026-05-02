package nous_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/adapters/httpx"
	"github.com/felixgeelhaar/olymp/internal/adapters/nous"
	"github.com/felixgeelhaar/olymp/internal/domain"
)

func nousFake(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/extract":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"considered":  0,
				"saved_ids":   []string{},
				"dropped":     0,
				"decision_id": "",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPlaybook_InjectsActionsForMatchingGoal(t *testing.T) {
	t.Parallel()
	srv := nousFake(t)
	c := nous.NewWithConfig(nous.Config{
		HTTP: httpx.Config{BaseURL: srv.URL, InitialDelay: time.Millisecond},
		Playbook: []nous.PlaybookEntry{
			{
				Match: "payments-latency",
				Actions: []domain.ActionRequest{
					{Capability: "http_request", Payload: map[string]any{"method": "POST", "url": "http://flaky-payments:9101/admin/heal"}},
				},
			},
		},
	})
	dec, err := c.Decide(context.Background(), domain.DecisionRequest{
		RunID: "r-1",
		Goal:  domain.Goal{Description: "remediate:payments-latency"},
	})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if len(dec.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(dec.Actions))
	}
	a := dec.Actions[0]
	if a.Capability != "http_request" {
		t.Errorf("capability = %q", a.Capability)
	}
	if a.ID == "" || a.IdempotencyKey == "" {
		t.Errorf("id / idem stamped: %+v", a)
	}
	if !strings.Contains(a.Payload["url"].(string), "/admin/heal") {
		t.Errorf("payload url = %v", a.Payload["url"])
	}
}

func TestPlaybook_NoMatchYieldsNoActions(t *testing.T) {
	t.Parallel()
	srv := nousFake(t)
	c := nous.NewWithConfig(nous.Config{
		HTTP: httpx.Config{BaseURL: srv.URL, InitialDelay: time.Millisecond},
		Playbook: []nous.PlaybookEntry{
			{Match: "payments-latency", Actions: []domain.ActionRequest{{Capability: "http_request"}}},
		},
	})
	dec, err := c.Decide(context.Background(), domain.DecisionRequest{
		RunID: "r-2",
		Goal:  domain.Goal{Description: "remediate:database-saturation"},
	})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if len(dec.Actions) != 0 {
		t.Errorf("actions = %d, want 0", len(dec.Actions))
	}
}

func TestPlaybook_EmptyPlaybookIsNoOp(t *testing.T) {
	t.Parallel()
	srv := nousFake(t)
	c := nous.New(httpx.Config{BaseURL: srv.URL, InitialDelay: time.Millisecond})
	dec, err := c.Decide(context.Background(), domain.DecisionRequest{
		RunID: "r-3",
		Goal:  domain.Goal{Description: "anything"},
	})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if len(dec.Actions) != 0 {
		t.Errorf("actions = %d, want 0", len(dec.Actions))
	}
}
