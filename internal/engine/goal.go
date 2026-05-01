package engine

import "github.com/felixgeelhaar/olymp/internal/domain"

// Satisfied returns true when the Goal's success criteria are met.
//
// Phase 1 supports a small kind set:
//   - "action_succeeded" — at least one action with the matching subject
//     reported status "succeeded"
//   - "user_accepted"    — present for explain-style intents; treated as a
//     no-op (always satisfied) so single-iteration loops can complete
//
// When Criteria is empty, the engine treats one full iteration as success.
// This keeps single-iteration `explain` flows from looping forever.
func Satisfied(goal domain.Goal, decision domain.DecisionRef, results []domain.ActionResult) bool {
	if len(goal.Criteria) == 0 {
		return true
	}
	for _, c := range goal.Criteria {
		if !criterionMet(c, decision, results) {
			return false
		}
	}
	return true
}

func criterionMet(c domain.GoalCriterion, _ domain.DecisionRef, results []domain.ActionResult) bool {
	switch c.Kind {
	case "action_succeeded":
		for _, r := range results {
			if c.Subject == "" || r.ActionID == c.Subject {
				if r.Status == "succeeded" {
					return true
				}
			}
		}
		return false
	case "user_accepted":
		return true
	default:
		// Unknown criteria fail closed — the loop iterates again.
		return false
	}
}
