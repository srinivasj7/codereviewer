// Package prompt assembles the LLM prompt from the diff, retrieved
// context, and applicable rules. The drop-order logic in budget.go
// enforces per-PR token caps without ever trimming the diff itself.
package prompt

import "strings"

// DefaultSystemPrompt is the cacheable prefix sent to the LLM. It is
// stable across calls so providers can prompt-cache it. See design §7.
const DefaultSystemPrompt = `You are a senior engineer reviewing a pull request.
You will receive: a unified diff, related code from the same repository,
past review comments with their outcomes, and applicable team rules.

Output ONLY a JSON array of comment objects with this exact shape:
  [{"file": "...", "start_line": N, "end_line": N, "comment": "...",
    "category": "bug|security|style|suggestion|question",
    "severity": "high|medium|low"}]

Rules for output:
- Cite exact line numbers from the diff. Never invent lines.
- Skip nits the team has previously dismissed (severity=low + dismissed pattern).
- Prefer questions over assertions when uncertain.
- Limit total comments to 8 per PR; pick the highest-severity findings.`

// DefaultClosingInstruction terminates the user-turn prompt.
const DefaultClosingInstruction = "Emit the JSON array now. No prose, no markdown fences."

// buildUserPrompt renders the user-turn payload in the canonical order:
// diff, related code, past reviews, context, rules, closing.
func buildUserPrompt(diff string, related, pastReviews []string, ctx []ContextSection, rules []string, closing string) string {
	var b strings.Builder
	b.WriteString("[DIFF]\n")
	b.WriteString(diff)
	b.WriteString("\n")
	if len(related) > 0 {
		b.WriteString("\n[RELATED CODE]\n")
		for _, c := range related {
			b.WriteString(c)
			b.WriteString("\n")
		}
	}
	if len(pastReviews) > 0 {
		b.WriteString("\n[PAST REVIEWS — with outcomes]\n")
		for _, r := range pastReviews {
			b.WriteString(r)
			b.WriteString("\n")
		}
	}
	if len(ctx) > 0 {
		b.WriteString("\n[CONTEXT]\n")
		for _, c := range ctx {
			b.WriteString("--- ")
			b.WriteString(c.Source)
			b.WriteString(": ")
			b.WriteString(c.Title)
			b.WriteString(" ---\n")
			b.WriteString(c.Body)
			b.WriteString("\n")
		}
	}
	if len(rules) > 0 {
		b.WriteString("\n[APPLICABLE RULES]\n")
		for _, r := range rules {
			b.WriteString(r)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(closing)
	return b.String()
}
