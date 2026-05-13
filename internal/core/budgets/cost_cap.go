// Package budgets enforces per-repo cost and token caps before any LLM
// call. A zero or negative daily cap means the bot is disabled for that
// repo and any review run short-circuits to the budget-exceeded path.
package budgets

// ExceedsDailyCap returns true when current spend already meets or
// exceeds the cap. A non-positive cap is treated as "disabled" — every
// run short-circuits.
func ExceedsDailyCap(spendUsd, capUsd float64) bool {
	if capUsd <= 0 {
		return true
	}
	return spendUsd >= capUsd
}
