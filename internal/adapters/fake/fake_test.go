package fake_test

import (
	"context"
	"errors"
	"testing"

	"github.com/felixgeelhaar/olymp/internal/adapters/fake"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

func TestMnemosFake(t *testing.T) {
	m := &fake.Mnemos{
		Memories: []domain.MemoryRef{{ID: "m-1"}},
		Index:    map[string]domain.MemoryRef{"m-1": {ID: "m-1", Kind: "claim"}},
	}
	mems, err := m.Recall(context.Background(), domain.MemoryQuery{RunID: "r"})
	if err != nil || len(mems) != 1 {
		t.Fatalf("recall got=%+v err=%v", mems, err)
	}
	if err := m.Append(context.Background(), domain.OutcomeEvent{RunID: "r"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got := m.Appended; len(got) != 1 || got[0].RunID != "r" {
		t.Fatalf("appended=%+v", got)
	}
	if _, err := m.Get(context.Background(), "missing"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestNousFakeScript(t *testing.T) {
	n := &fake.Nous{
		Script:           []domain.DecisionRef{{ID: "d-1"}, {ID: "d-2"}},
		ScriptedDecision: domain.DecisionRef{ID: "default"},
	}
	d1, _ := n.Decide(context.Background(), domain.DecisionRequest{RunID: "r"})
	d2, _ := n.Decide(context.Background(), domain.DecisionRequest{RunID: "r"})
	d3, _ := n.Decide(context.Background(), domain.DecisionRequest{RunID: "r"})
	if d1.ID != "d-1" || d2.ID != "d-2" || d3.ID != "default" {
		t.Fatalf("script consumed wrong: %s, %s, %s", d1.ID, d2.ID, d3.ID)
	}
}

func TestPraxisFakeRecordsCalls(t *testing.T) {
	p := &fake.Praxis{Result: domain.ActionResult{Status: "succeeded"}}
	res, err := p.Execute(context.Background(), domain.ActionRequest{ID: "a-1", Capability: "x"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.ActionID != "a-1" || res.Status != "succeeded" {
		t.Fatalf("res=%+v", res)
	}
	if len(p.Calls) != 1 {
		t.Fatalf("calls=%d want 1", len(p.Calls))
	}
}
