package feedback_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"codereviewer/internal/adapters/clocksystem"
	"codereviewer/internal/adapters/obsstdout"
	"codereviewer/internal/core/pipelines/feedback"
	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
	"codereviewer/internal/testing/fakes"
)

// fakeLlm is the minimum LlmGateway implementation we need for the
// conversation tests. It returns a fixed body so tests can assert
// against it; per-test override via Content.
type fakeLlm struct {
	Content string
	Calls   int
}

func (f *fakeLlm) Chat(_ context.Context, _ ports.ChatRequest) (ports.ChatResponse, error) {
	f.Calls++
	body := f.Content
	if body == "" {
		body = "Sure — what I meant was the err return swallows the upstream cause."
	}
	return ports.ChatResponse{Content: body, TokensIn: 50, TokensOut: 20, CostUsd: 0.0001, ModelUsed: "primary"}, nil
}
func (f *fakeLlm) Embed(_ context.Context, _ []string, _ ports.EmbedOpts) ([]ports.EmbeddingResult, error) {
	return nil, nil
}
func (f *fakeLlm) EstimateTokens(text, _ string) int { return len(text) / 4 }

func newConversationPipeline(t *testing.T, cfg schemas.ConversationConfig) (
	*feedback.Pipeline, *fakes.CommentStore, *fakes.Vcs, *fakeLlm,
) {
	t.Helper()
	comments := fakes.NewCommentStore()
	fb := fakes.NewFeedbackStore()
	vcs := fakes.NewVcs()
	llm := &fakeLlm{}
	caps := fakes.NewCostCapStore()
	p := feedback.NewPipeline(feedback.Deps{
		Comments: comments,
		Feedback: fb,
		Clock:    clocksystem.New(),
		Obs:      obsstdout.New("test"),
	})
	p.SetConversationDeps(vcs, llm, caps, func() schemas.ConversationConfig { return cfg })
	return p, comments, vcs, llm
}

func seedBotParent(t *testing.T, cs *fakes.CommentStore, prNumber int, githubId int64, text string) {
	t.Helper()
	gid := githubId
	_, err := cs.Upsert(context.Background(), store.CommentUpsert{
		TenantId:    tenant,
		RepoId:      repo,
		PrNumber:    prNumber,
		Source:      "bot",
		GithubId:    &gid,
		CommentText: text,
		Outcome:     store.OutcomePending,
	})
	require.NoError(t, err)
}

func replyJob(commentExternalId int64, prNumber int, body string) []byte {
	j := schemas.FeedbackJob{
		TenantId:          tenant,
		RepoId:            repo,
		Kind:              "reply",
		CommentExternalId: commentExternalId,
		Body:              body,
		PrNumber:          prNumber,
	}
	b, _ := json.Marshal(j)
	return b
}

func TestConversation_RepliesToQuestionMark(t *testing.T) {
	cfg := schemas.ConversationConfig{
		Enabled:         true,
		MaxRepliesPerPr: 2,
		TriggerSuffixes: []string{"?"},
		TriggerPrefixes: []string{"/explain"},
		MaxOutputTokens: 300,
	}
	p, comments, vcs, llm := newConversationPipeline(t, cfg)
	seedBotParent(t, comments, 7, 555, "consider returning an error here")

	err := p.Handle(context.Background(),
		replyJob(555, 7, "why is that wrong here?"),
		ports.ConsumeCtx{Ack: func() error { return nil }, Nack: func(string) error { return nil }})
	require.NoError(t, err)

	require.Equal(t, 1, llm.Calls, "LLM should be called once")
	require.Len(t, vcs.PostCommentReplies(), 1, "VCS should receive one reply post")
	c := vcs.PostCommentReplies()[0]
	require.Equal(t, int64(555), c.ParentCommentId)
	require.Equal(t, 7, c.PrNumber)

	// Bot-reply should have been persisted for cap counting on next reply.
	all, _ := comments.ListByPr(context.Background(), ports.PrRef{TenantId: tenant, RepoId: repo, PrNumber: 7})
	var replies int
	for _, c := range all {
		if c.Source == "bot-reply" {
			replies++
		}
	}
	require.Equal(t, 1, replies)
}

func TestConversation_SkipsNonTriggerReply(t *testing.T) {
	cfg := schemas.ConversationConfig{
		Enabled:         true,
		MaxRepliesPerPr: 2,
		TriggerSuffixes: []string{"?"},
		TriggerPrefixes: []string{"/explain"},
		MaxOutputTokens: 300,
	}
	p, comments, vcs, llm := newConversationPipeline(t, cfg)
	seedBotParent(t, comments, 7, 555, "...")

	err := p.Handle(context.Background(),
		replyJob(555, 7, "agreed, will fix"),
		ports.ConsumeCtx{Ack: func() error { return nil }, Nack: func(string) error { return nil }})
	require.NoError(t, err)

	require.Equal(t, 0, llm.Calls, "non-trigger reply should not call LLM")
	require.Empty(t, vcs.PostCommentReplies(), "non-trigger reply should not post")
}

func TestConversation_EnforcesPerPrCap(t *testing.T) {
	cfg := schemas.ConversationConfig{
		Enabled:         true,
		MaxRepliesPerPr: 1, // cap at 1 so the second reply gets skipped
		TriggerSuffixes: []string{"?"},
		MaxOutputTokens: 300,
	}
	p, comments, vcs, llm := newConversationPipeline(t, cfg)
	seedBotParent(t, comments, 7, 555, "...")

	// First reply — should be answered.
	require.NoError(t, p.Handle(context.Background(),
		replyJob(555, 7, "why?"),
		ports.ConsumeCtx{Ack: func() error { return nil }, Nack: func(string) error { return nil }}))
	require.Equal(t, 1, llm.Calls)
	require.Len(t, vcs.PostCommentReplies(), 1)

	// Second reply — cap reached.
	require.NoError(t, p.Handle(context.Background(),
		replyJob(555, 7, "still curious why?"),
		ports.ConsumeCtx{Ack: func() error { return nil }, Nack: func(string) error { return nil }}))
	require.Equal(t, 1, llm.Calls, "cap should block second LLM call")
	require.Len(t, vcs.PostCommentReplies(), 1, "cap should block second post")
}

func TestConversation_DisabledByConfig(t *testing.T) {
	cfg := schemas.ConversationConfig{Enabled: false}
	p, comments, vcs, llm := newConversationPipeline(t, cfg)
	seedBotParent(t, comments, 7, 555, "...")

	require.NoError(t, p.Handle(context.Background(),
		replyJob(555, 7, "why?"),
		ports.ConsumeCtx{Ack: func() error { return nil }, Nack: func(string) error { return nil }}))
	require.Equal(t, 0, llm.Calls)
	require.Empty(t, vcs.PostCommentReplies())
}

func TestConversation_ExplainPrefix(t *testing.T) {
	cfg := schemas.ConversationConfig{
		Enabled:         true,
		MaxRepliesPerPr: 2,
		TriggerPrefixes: []string{"/explain"},
		TriggerSuffixes: []string{"?"},
		MaxOutputTokens: 300,
	}
	p, comments, vcs, llm := newConversationPipeline(t, cfg)
	seedBotParent(t, comments, 7, 555, "...")

	require.NoError(t, p.Handle(context.Background(),
		replyJob(555, 7, "/explain please"),
		ports.ConsumeCtx{Ack: func() error { return nil }, Nack: func(string) error { return nil }}))
	require.Equal(t, 1, llm.Calls)
	require.Len(t, vcs.PostCommentReplies(), 1)
}
