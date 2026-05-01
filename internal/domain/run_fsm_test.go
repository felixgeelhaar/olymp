package domain

import (
	"testing"

	"github.com/felixgeelhaar/statekit"
)

func currentStatus(m *statekit.Interpreter[struct{}]) RunStatus {
	return RunStatus(m.State().Value)
}

// TestRunMachine_HappyPath drives one full iteration of the loop end-to-end
// through the FSM and asserts each transition lands in the expected state.
func TestRunMachine_HappyPath(t *testing.T) {
	m, err := NewRunMachine()
	if err != nil {
		t.Fatalf("build machine: %v", err)
	}
	m.Start()
	for _, target := range []RunStatus{
		StatusObserving,
		StatusUnderstanding,
		StatusDeciding,
		StatusActing,
		StatusLearning,
		StatusCompleted,
	} {
		m.Send(statekit.Event{Type: ev(target)})
		if got := currentStatus(m); got != target {
			t.Fatalf("after %s: current=%s want %s", target, got, target)
		}
	}
}

func TestRunMachine_ApprovalGate(t *testing.T) {
	m, err := NewRunMachine()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m.Start()
	for _, target := range []RunStatus{
		StatusObserving, StatusUnderstanding, StatusDeciding,
		StatusAwaitingApproval, StatusActing, StatusLearning, StatusCompleted,
	} {
		m.Send(statekit.Event{Type: ev(target)})
	}
	if got := currentStatus(m); got != StatusCompleted {
		t.Fatalf("current=%s want completed", got)
	}
}

func TestRunMachine_LearningLoopsBack(t *testing.T) {
	m, err := NewRunMachine()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m.Start()
	for _, target := range []RunStatus{
		StatusObserving, StatusUnderstanding, StatusDeciding,
		StatusActing, StatusLearning, StatusObserving, // next iteration
	} {
		m.Send(statekit.Event{Type: ev(target)})
	}
	if got := currentStatus(m); got != StatusObserving {
		t.Fatalf("current=%s want observing (next iteration)", got)
	}
}

func TestRunMachine_PauseResume(t *testing.T) {
	m, err := NewRunMachine()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m.Start()
	for _, target := range []RunStatus{StatusObserving, StatusUnderstanding, StatusPaused, StatusDeciding} {
		m.Send(statekit.Event{Type: ev(target)})
	}
	if got := currentStatus(m); got != StatusDeciding {
		t.Fatalf("current=%s want deciding", got)
	}
}

func TestRunMachine_TerminalStatesAreFinal(t *testing.T) {
	m, err := NewRunMachine()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m.Start()
	for _, target := range []RunStatus{StatusObserving, StatusFailed} {
		m.Send(statekit.Event{Type: ev(target)})
	}
	// Any further send must not change state away from failed.
	m.Send(statekit.Event{Type: ev(StatusObserving)})
	if got := currentStatus(m); got != StatusFailed {
		t.Fatalf("after invalid transition: current=%s want failed", got)
	}
}

func TestRunMachine_RejectsInvalidTransition(t *testing.T) {
	m, err := NewRunMachine()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m.Start()
	// pending → understanding is not allowed; pending only → observing | cancelled.
	m.Send(statekit.Event{Type: ev(StatusUnderstanding)})
	if got := currentStatus(m); got != StatusPending {
		t.Fatalf("invalid transition consumed: current=%s want pending", got)
	}
}
