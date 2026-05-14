package prompt

// Section identifies an optional prompt segment that can be dropped under
// token pressure. Drop order is fixed:
//
//	PastReviews → RelatedCode → Context → Rules
//
// The diff is NEVER dropped. Context (issues, repo conventions, ad-hoc
// operator notes) drops before Rules because Rules apply across all PRs
// while Context is one-shot — but Context outlives RelatedCode because
// issue descriptions are unreplaceable while related code overlaps the
// diff already.
type Section int

// Sections that may be dropped under token pressure.
const (
	SectionPastReviews Section = iota
	SectionRelatedCode
	SectionContext
	SectionRules
)

// String returns a stable label suitable for logs and metrics.
func (s Section) String() string {
	switch s {
	case SectionPastReviews:
		return "past_reviews"
	case SectionRelatedCode:
		return "related_code"
	case SectionContext:
		return "context"
	case SectionRules:
		return "rules"
	default:
		return "unknown"
	}
}

// Inputs are the raw materials for prompt assembly.
type Inputs struct {
	SystemPrompt       string
	Diff               string
	RelatedCode        []string
	PastReviews        []string
	Context            []ContextSection
	Rules              []string
	ClosingInstruction string
}

// ContextSection is one heading + body pair. The assembler renders
// each as a labeled sub-section under the top-level CONTEXT heading.
type ContextSection struct {
	Source string // "jira" | "github-issues" | "repo-instructions" | ...
	Title  string
	Body   string
}

// Assembled is the result of Assemble. UserPrompt is the rendered user-turn
// payload; Dropped lists segments dropped under budget pressure;
// DiffOverflow is true when the diff alone exceeds the cap and the caller
// must chunk the diff (per design §7).
type Assembled struct {
	SystemPrompt    string
	UserPrompt      string
	TokensEstimated int
	Dropped         []Section
	DiffOverflow    bool
}

// TokenEstimator returns an estimated token count for a string. The
// pipeline supplies a tiktoken-based implementation via the LLM gateway.
type TokenEstimator func(string) int

// Assemble renders the prompt, dropping optional sections in fixed order
// until the total fits in tokenCap. If even the diff exceeds the cap,
// DiffOverflow is set and the caller MUST chunk the diff before posting.
func Assemble(in Inputs, tokenCap int, est TokenEstimator) Assembled {
	related := in.RelatedCode
	reviews := in.PastReviews
	rules := in.Rules
	contextSec := in.Context
	var drops []Section

	for {
		user := buildUserPrompt(in.Diff, related, reviews, contextSec, rules, in.ClosingInstruction)
		total := est(in.SystemPrompt) + est(user)
		if total <= tokenCap {
			return Assembled{
				SystemPrompt:    in.SystemPrompt,
				UserPrompt:      user,
				TokensEstimated: total,
				Dropped:         drops,
				DiffOverflow:    false,
			}
		}
		switch {
		case len(reviews) > 0:
			reviews = nil
			drops = append(drops, SectionPastReviews)
		case len(related) > 0:
			related = nil
			drops = append(drops, SectionRelatedCode)
		case len(contextSec) > 0:
			contextSec = nil
			drops = append(drops, SectionContext)
		case len(rules) > 0:
			rules = nil
			drops = append(drops, SectionRules)
		default:
			return Assembled{
				SystemPrompt:    in.SystemPrompt,
				UserPrompt:      user,
				TokensEstimated: total,
				Dropped:         drops,
				DiffOverflow:    true,
			}
		}
	}
}
