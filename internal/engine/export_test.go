package engine

import "github.com/felixgeelhaar/olymp/internal/domain"

// RequiresApprovalForTest exposes requiresApproval to internal tests.
func RequiresApprovalForTest(t domain.IntentType, d domain.DecisionRef) bool {
	return requiresApproval(t, d)
}
