package feedback

import (
	"context"
	"strings"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
)

// conversationDeps is the subset of Deps the conversation sub-handler
// uses. Kept private so callers wire via the main Deps struct.
type conversationDeps struct {
	Vcs      ports.VcsRegistry
	Llm      ports.LlmGateway
	Comments store.CommentStore
	CostCaps store.CostCapStore
	Clock    ports.Clock
	Obs      ports.Obs
	Config   func() schemas.ConversationConfig
}

// maybeReply runs after the feedback outcome is recorded. If the
// conversation feature is enabled and the reply matches a trigger, it
// runs a focused LLM call and posts a nested reply on the parent bot
// comment. Per-PR reply cap and per-tenant daily USD cap both apply.
//
// All exits log a one-line reason — this path is the bot speaking, and
// the operator needs to see why it did or didn't.
func (p *Pipeline) maybeReply(ctx context.Context, parent store.Comment, job schemas.FeedbackJob) {
	if p.conv.Llm == nil || p.conv.Vcs == nil || p.conv.Config == nil {
		// Deps not wired — feedback worker booted without the conversation
		// pipeline configured. Stay silent, this is the pre-slice-8
		// behavior.
		return
	}
	cfg := p.conv.Config()
	if !cfg.Enabled {
		return
	}
	if job.Kind != "reply" || strings.TrimSpace(job.Body) == "" {
		return
	}
	if !matchesTrigger(job.Body, cfg) {
		p.conv.Obs.Logger.Info("conversation: reply did not match trigger",
			"comment_id", string(parent.CommentId), "pr_number", job.PrNumber)
		return
	}

	// Per-PR reply cap — count existing bot-reply rows for this PR.
	prRef := ports.PrRef{TenantId: parent.TenantId, RepoId: parent.RepoId, PrNumber: job.PrNumber}
	existing, err := p.conv.Comments.ListByPr(ctx, prRef)
	if err != nil {
		p.conv.Obs.Logger.Warn("conversation: list pr comments failed", "err", err.Error())
		return
	}
	count := 0
	for _, c := range existing {
		if c.Source == "bot-reply" {
			count++
		}
	}
	if count >= cfg.MaxRepliesPerPr {
		p.conv.Obs.Logger.Info("conversation: per-PR reply cap reached; skipping",
			"pr_number", job.PrNumber, "cap", cfg.MaxRepliesPerPr, "count", count)
		return
	}

	// Daily cost cap — same circuit breaker as the review pipeline.
	if p.conv.CostCaps != nil {
		cap, err := p.conv.CostCaps.GetEffective(ctx, parent.TenantId, parent.RepoId)
		if err == nil && cap.DailyUsdCap > 0 {
			spend, _ := p.conv.CostCaps.TodaySpend(ctx, parent.TenantId, parent.RepoId, "UTC")
			if spend >= cap.DailyUsdCap {
				p.conv.Obs.Logger.Info("conversation: daily cost cap reached; skipping",
					"pr_number", job.PrNumber, "spend", spend, "cap", cap.DailyUsdCap)
				return
			}
		}
	}

	// LLM call. System prompt is intentionally narrow — clarify the
	// original concern; do not introduce new criticisms.
	resp, err := p.conv.Llm.Chat(ctx, ports.ChatRequest{
		Tier:            ports.LlmTierPrimary,
		SystemPrompt:    conversationSystemPrompt,
		UserPrompt:      buildConversationUserPrompt(parent.CommentText, job.Body),
		MaxOutputTokens: cfg.MaxOutputTokens,
		ResponseFormat:  "text",
	})
	if err != nil {
		p.conv.Obs.Logger.Warn("conversation: llm chat failed", "err", err.Error(),
			"pr_number", job.PrNumber)
		return
	}
	reply := strings.TrimSpace(resp.Content)
	if reply == "" {
		p.conv.Obs.Logger.Warn("conversation: llm returned empty body; skipping post",
			"pr_number", job.PrNumber)
		return
	}

	// Post the reply on the VCS. Parent external id is whatever we
	// stored when the review pipeline persisted the original comment.
	var parentExternalId int64
	if parent.GithubId != nil {
		parentExternalId = *parent.GithubId
	}
	if parentExternalId == 0 {
		p.conv.Obs.Logger.Warn("conversation: parent comment has no external id; cannot thread reply",
			"comment_id", string(parent.CommentId))
		return
	}
	vcs, err := p.conv.Vcs.For(job.Provider)
	if err != nil {
		p.conv.Obs.Logger.Warn("conversation: vcs registry lookup failed",
			"provider", string(job.Provider), "err", err.Error())
		return
	}
	newId, err := vcs.PostCommentReply(ctx, parent.RepoId, job.PrNumber, parentExternalId, reply)
	if err != nil {
		p.conv.Obs.Logger.Warn("conversation: post reply failed", "err", err.Error())
		return
	}

	// Persist the bot-reply so future reply-to-reply cycles see it for
	// the per-PR cap.
	newIdCopy := newId
	if _, err := p.conv.Comments.Upsert(ctx, store.CommentUpsert{
		TenantId:    parent.TenantId,
		RepoId:      parent.RepoId,
		PrNumber:    job.PrNumber,
		Source:      "bot-reply",
		GithubId:    &newIdCopy,
		CommentText: reply,
	}); err != nil {
		p.conv.Obs.Logger.Warn("conversation: persist bot-reply failed", "err", err.Error())
	}

	// Charge the spend against the daily cap.
	if p.conv.CostCaps != nil {
		if err := p.conv.CostCaps.RecordSpend(ctx, parent.TenantId, parent.RepoId, resp.CostUsd, p.conv.Clock.Now()); err != nil {
			p.conv.Obs.Logger.Warn("conversation: record spend failed", "err", err.Error())
		}
	}

	p.conv.Obs.Logger.Info("conversation: posted reply",
		"parent_external_id", parentExternalId,
		"new_external_id", newId,
		"tokens_in", resp.TokensIn,
		"tokens_out", resp.TokensOut,
		"cost_usd", resp.CostUsd,
		"pr_number", job.PrNumber)
}

func matchesTrigger(body string, cfg schemas.ConversationConfig) bool {
	trimmed := strings.TrimSpace(body)
	for _, p := range cfg.TriggerPrefixes {
		if p == "" {
			continue
		}
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	for _, s := range cfg.TriggerSuffixes {
		if s == "" {
			continue
		}
		if strings.HasSuffix(trimmed, s) {
			return true
		}
	}
	return false
}

const conversationSystemPrompt = `You are a senior engineer who wrote an
inline review comment. The author of the pull request has replied with
a follow-up question or asked you to explain.

Provide a concise clarification (under ~200 words). Stay focused on the
original concern. Do not introduce new criticisms. Do not repeat your
original wording verbatim. Plain text only — no Markdown headings, no
code fences unless quoting code is essential.

If the reply is asking you to retract or you no longer believe the
concern was valid given the new context, say so plainly.`

func buildConversationUserPrompt(originalComment, reply string) string {
	var b strings.Builder
	b.WriteString("Your original comment:\n")
	b.WriteString(originalComment)
	b.WriteString("\n\nThe author replied:\n")
	b.WriteString(reply)
	b.WriteString("\n\nClarify.")
	return b.String()
}

// SetConversationDeps wires the conversation sub-handler. Called from
// the feedback-worker boot once after the main NewPipeline. Kept as a
// post-construction setter so the existing Deps struct stays stable
// for callers that don't need conversation mode (e.g. the smoke
// harness, the in-memory feedback tests).
func (p *Pipeline) SetConversationDeps(vcs ports.VcsRegistry, llm ports.LlmGateway, costCaps store.CostCapStore, conf func() schemas.ConversationConfig) {
	p.conv.Vcs = vcs
	p.conv.Llm = llm
	p.conv.CostCaps = costCaps
	p.conv.Config = conf
	// Reuse the main deps' clock + obs + comments — they're already
	// available on the receiver.
	p.conv.Clock = p.deps.Clock
	p.conv.Obs = p.deps.Obs
	p.conv.Comments = p.deps.Comments
}

