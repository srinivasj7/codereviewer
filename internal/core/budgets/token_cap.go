package budgets

// Default token caps. PerPrTokenCap caps the input; MaxOutputTokens caps
// the output. Both are overridable per repo via cost_caps.
const (
	DefaultPerPrTokenCap  = 30000
	DefaultMaxOutputTokens = 4000
)

// MaxOutputTokens returns a safe MaxOutputTokens given the input cap.
// When the input cap is small, we shrink the output budget proportionally
// so the model isn't asked for more output than the run is sized for.
func MaxOutputTokens(perPrTokenCap int) int {
	if perPrTokenCap <= 0 {
		return DefaultMaxOutputTokens
	}
	if perPrTokenCap < DefaultMaxOutputTokens*2 {
		out := perPrTokenCap / 4
		if out < 200 {
			return 200
		}
		return out
	}
	return DefaultMaxOutputTokens
}
