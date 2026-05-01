// Package fake provides in-memory fakes of every cognitive-layer port. Used
// by loop-engine tests to drive the runtime end-to-end without HTTP servers.
//
// Fakes are intentionally simple: scripted responses keyed by call shape, no
// network, no goroutines. They satisfy ports.{Mnemos,Chronos,Nous,Praxis}Port.
package fake

import (
	"context"
	"fmt"
	"sync"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// Mnemos is a fake ports.MnemosPort. Recall returns Memories; Append records
// every event in Appended; Get serves from Index.
type Mnemos struct {
	mu       sync.Mutex
	Memories []domain.MemoryRef
	Appended []domain.OutcomeEvent
	Index    map[string]domain.MemoryRef
	Err      error
}

func (m *Mnemos) Recall(_ context.Context, _ domain.MemoryQuery) ([]domain.MemoryRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return nil, m.Err
	}
	out := append([]domain.MemoryRef(nil), m.Memories...)
	return out, nil
}

func (m *Mnemos) Append(_ context.Context, e domain.OutcomeEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	m.Appended = append(m.Appended, e)
	return nil
}

func (m *Mnemos) Get(_ context.Context, id string) (domain.MemoryRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return domain.MemoryRef{}, m.Err
	}
	if r, ok := m.Index[id]; ok {
		return r, nil
	}
	return domain.MemoryRef{}, fmt.Errorf("fake mnemos: %s: %w", id, ports.ErrNotFound)
}

// Chronos is a fake ports.ChronosPort.
type Chronos struct {
	mu       sync.Mutex
	Signals_ []domain.SignalRef
	Index    map[string]domain.SignalRef
	Err      error
}

func (c *Chronos) Signals(_ context.Context, _ domain.SignalQuery) ([]domain.SignalRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return nil, c.Err
	}
	return append([]domain.SignalRef(nil), c.Signals_...), nil
}

func (c *Chronos) Get(_ context.Context, id string) (domain.SignalRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return domain.SignalRef{}, c.Err
	}
	if s, ok := c.Index[id]; ok {
		return s, nil
	}
	return domain.SignalRef{}, fmt.Errorf("fake chronos: %s: %w", id, ports.ErrNotFound)
}

// Nous is a fake ports.NousPort. Decisions are scripted and consumed in order
// per call; ScriptedDecision is reused once the script is exhausted.
type Nous struct {
	mu               sync.Mutex
	Script           []domain.DecisionRef
	ScriptedDecision domain.DecisionRef
	LastRequest      domain.DecisionRequest
	Err              error
}

func (n *Nous) Decide(_ context.Context, in domain.DecisionRequest) (domain.DecisionRef, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.LastRequest = in
	if n.Err != nil {
		return domain.DecisionRef{}, n.Err
	}
	if len(n.Script) > 0 {
		d := n.Script[0]
		n.Script = n.Script[1:]
		return d, nil
	}
	return n.ScriptedDecision, nil
}

func (n *Nous) Get(_ context.Context, id string) (domain.DecisionRef, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Err != nil {
		return domain.DecisionRef{}, n.Err
	}
	if id == n.ScriptedDecision.ID {
		return n.ScriptedDecision, nil
	}
	for _, d := range n.Script {
		if d.ID == id {
			return d, nil
		}
	}
	return domain.DecisionRef{}, fmt.Errorf("fake nous: %s: %w", id, ports.ErrNotFound)
}

// Praxis is a fake ports.PraxisPort. Execute records every action in Calls
// and returns the configured Result (or Err).
type Praxis struct {
	mu           sync.Mutex
	Capabilities []domain.CapabilityRef
	Calls        []domain.ActionRequest
	Result       domain.ActionResult
	Sim          domain.SimulationRef
	Err          error
}

func (p *Praxis) ListCapabilities(_ context.Context) ([]domain.CapabilityRef, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Err != nil {
		return nil, p.Err
	}
	return append([]domain.CapabilityRef(nil), p.Capabilities...), nil
}

func (p *Praxis) Execute(_ context.Context, a domain.ActionRequest) (domain.ActionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Calls = append(p.Calls, a)
	if p.Err != nil {
		return domain.ActionResult{}, p.Err
	}
	r := p.Result
	if r.ActionID == "" {
		r.ActionID = a.ID
	}
	return r, nil
}

func (p *Praxis) DryRun(_ context.Context, a domain.ActionRequest) (domain.SimulationRef, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Calls = append(p.Calls, a)
	if p.Err != nil {
		return domain.SimulationRef{}, p.Err
	}
	s := p.Sim
	if s.ActionID == "" {
		s.ActionID = a.ID
	}
	return s, nil
}

// Compile-time assertions.
var (
	_ ports.MnemosPort  = (*Mnemos)(nil)
	_ ports.ChronosPort = (*Chronos)(nil)
	_ ports.NousPort    = (*Nous)(nil)
	_ ports.PraxisPort  = (*Praxis)(nil)
)
