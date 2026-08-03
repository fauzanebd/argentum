package bootstrap

import "testing"

// The SDK keeps maxIterations unexported and offers no getter, so the choice is
// pinned here, at the only seam a test can reach it.
func TestTurnMaxIterations(t *testing.T) {
	tests := []struct {
		name             string
		turn, deployment int
		want             int
	}{
		// The case the whole change is for: a document turn carries
		// agentbudget.ForDocument's ceiling, and the provider's loop has to run
		// under that rather than under the number this process booted with.
		{"the turn's budget wins", 12, 8, 12},
		// Every caller outside a chat turn — the composition tests, anything
		// building an agent without a budget — keeps today's behaviour.
		{"no budget falls back to the deployment", 0, 8, 8},
		{"a tuned budget below the deployment is respected", 3, 8, 3},
		{"a negative is treated as unset", -1, 8, 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := turnMaxIterations(tt.turn, tt.deployment); got != tt.want {
				t.Errorf("turnMaxIterations(%d, %d) = %d, want %d",
					tt.turn, tt.deployment, got, tt.want)
			}
		})
	}
}
