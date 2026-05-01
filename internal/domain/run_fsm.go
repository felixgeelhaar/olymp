package domain

import "github.com/felixgeelhaar/statekit"

// state and ev convert RunStatus to statekit's typed identifiers.
func state(s RunStatus) statekit.StateID { return statekit.StateID(s) }
func ev(s RunStatus) statekit.EventType  { return statekit.EventType(s) }

// NewRunMachine returns the state machine that governs RunStatus transitions.
//
// The transition table mirrors the TDD §3.1 contract. The loop engine drives
// transitions through this machine instead of mutating Run.Status freely; any
// invalid transition surfaces as an error from the interpreter.
func NewRunMachine() (*statekit.Interpreter[struct{}], error) {
	machine, err := statekit.NewMachine[struct{}]("olymp.run").
		WithInitial(state(StatusPending)).
		State(state(StatusPending)).
		On(ev(StatusObserving)).Target(state(StatusObserving)).
		On(ev(StatusCancelled)).Target(state(StatusCancelled)).
		Done().
		State(state(StatusObserving)).
		On(ev(StatusUnderstanding)).Target(state(StatusUnderstanding)).
		On(ev(StatusPaused)).Target(state(StatusPaused)).
		On(ev(StatusFailed)).Target(state(StatusFailed)).
		Done().
		State(state(StatusUnderstanding)).
		On(ev(StatusDeciding)).Target(state(StatusDeciding)).
		On(ev(StatusPaused)).Target(state(StatusPaused)).
		On(ev(StatusFailed)).Target(state(StatusFailed)).
		Done().
		State(state(StatusDeciding)).
		On(ev(StatusActing)).Target(state(StatusActing)).
		On(ev(StatusAwaitingApproval)).Target(state(StatusAwaitingApproval)).
		On(ev(StatusLearning)).Target(state(StatusLearning)).
		On(ev(StatusPaused)).Target(state(StatusPaused)).
		On(ev(StatusFailed)).Target(state(StatusFailed)).
		Done().
		State(state(StatusAwaitingApproval)).
		On(ev(StatusActing)).Target(state(StatusActing)).
		On(ev(StatusCancelled)).Target(state(StatusCancelled)).
		Done().
		State(state(StatusActing)).
		On(ev(StatusLearning)).Target(state(StatusLearning)).
		On(ev(StatusPaused)).Target(state(StatusPaused)).
		On(ev(StatusFailed)).Target(state(StatusFailed)).
		Done().
		State(state(StatusLearning)).
		On(ev(StatusObserving)).Target(state(StatusObserving)). // next iteration
		On(ev(StatusCompleted)).Target(state(StatusCompleted)).
		On(ev(StatusFailed)).Target(state(StatusFailed)).
		Done().
		State(state(StatusPaused)).
		On(ev(StatusObserving)).Target(state(StatusObserving)).
		On(ev(StatusUnderstanding)).Target(state(StatusUnderstanding)).
		On(ev(StatusDeciding)).Target(state(StatusDeciding)).
		On(ev(StatusActing)).Target(state(StatusActing)).
		On(ev(StatusLearning)).Target(state(StatusLearning)).
		On(ev(StatusCancelled)).Target(state(StatusCancelled)).
		Done().
		State(state(StatusCompleted)).Final().Done().
		State(state(StatusFailed)).Final().Done().
		State(state(StatusCancelled)).Final().Done().
		Build()
	if err != nil {
		return nil, err
	}
	return statekit.NewInterpreter(machine), nil
}
