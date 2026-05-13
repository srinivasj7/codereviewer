package feedback_test

import (
	"context"
	"encoding/json"
	"errors"
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

const (
	tenant       = ports.TenantId("t-feedback")
	repo         = ports.RepoId("acme/widgets")
	botGithubId  = int64(101)
	userGithubId = int64(202)
)

func newPipeline(t *testing.T) (*feedback.Pipeline, *fakes.CommentStore, *fakes.FeedbackStore) {
	t.Helper()
	comments := fakes.NewCommentStore()
	fb := fakes.NewFeedbackStore()
	p := feedback.NewPipeline(feedback.Deps{
		Comments: comments,
		Feedback: fb,
		Clock:    clocksystem.New(),
		Obs:      obsstdout.New("test"),
	})
	return p, comments, fb
}

func seedBotComment(t *testing.T, cs *fakes.CommentStore, githubId int64) store.CommentId {
	t.Helper()
	gid := githubId
	id, err := cs.Upsert(context.Background(), store.CommentUpsert{
		TenantId:    tenant,
		RepoId:      repo,
		PrNumber:    7,
		Source:      "bot",
		GithubId:    &gid,
		CommentText: "consider returning an error here",
		Outcome:     store.OutcomePending,
	})
	require.NoError(t, err)
	return id
}

type capturingAckNack struct {
	acked  bool
	nacked string
}

func (c *capturingAckNack) ctx() ports.ConsumeCtx {
	return ports.ConsumeCtx{
		Ack:  func() error { c.acked = true; return nil },
		Nack: func(reason string) error { c.nacked = reason; return nil },
	}
}

func runJob(t *testing.T, p *feedback.Pipeline, job schemas.FeedbackJob) *capturingAckNack {
	t.Helper()
	payload, err := json.Marshal(job)
	require.NoError(t, err)
	cap := &capturingAckNack{}
	err = p.Handle(context.Background(), payload, cap.ctx())
	require.NoError(t, err)
	return cap
}

func TestThumbsUpReaction(t *testing.T) {
	p, cs, fb := newPipeline(t)
	commentId := seedBotComment(t, cs, botGithubId)

	cap := runJob(t, p, schemas.FeedbackJob{
		TenantId:          tenant,
		RepoId:            repo,
		Kind:              "reaction",
		CommentExternalId: botGithubId,
		Reaction:          "+1",
	})
	require.True(t, cap.acked)

	events, err := fb.ListForComment(context.Background(), commentId)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, store.SignalThumbsUp, events[0].Signal)

	all := cs.AllComments()
	require.Len(t, all, 1)
	require.Equal(t, store.OutcomeAccepted, all[0].Outcome)
}

func TestThumbsDownReaction(t *testing.T) {
	p, cs, fb := newPipeline(t)
	commentId := seedBotComment(t, cs, botGithubId)

	runJob(t, p, schemas.FeedbackJob{
		Kind:              "reaction",
		CommentExternalId: botGithubId,
		Reaction:          "-1",
	})

	events, err := fb.ListForComment(context.Background(), commentId)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, store.SignalThumbsDown, events[0].Signal)
	require.Equal(t, store.OutcomeDismissed, cs.AllComments()[0].Outcome)
}

func TestReplyTreatedAsDiscussion(t *testing.T) {
	p, cs, fb := newPipeline(t)
	commentId := seedBotComment(t, cs, botGithubId)

	runJob(t, p, schemas.FeedbackJob{
		Kind:              "reply",
		CommentExternalId: botGithubId,
		AuthorId:          "alice",
	})

	events, err := fb.ListForComment(context.Background(), commentId)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, store.SignalReplied, events[0].Signal)
	require.Equal(t, store.OutcomeDiscussed, cs.AllComments()[0].Outcome)
}

func TestUnknownReactionIgnored(t *testing.T) {
	p, cs, fb := newPipeline(t)
	seedBotComment(t, cs, botGithubId)

	cap := runJob(t, p, schemas.FeedbackJob{
		Kind:              "reaction",
		CommentExternalId: botGithubId,
		Reaction:          "eyes",
	})
	require.True(t, cap.acked)

	require.Equal(t, store.OutcomePending, cs.AllComments()[0].Outcome, "outcome must stay pending")

	events, err := fb.ListForComment(context.Background(), cs.AllComments()[0].CommentId)
	require.NoError(t, err)
	require.Empty(t, events)
}

func TestUnknownCommentAcked(t *testing.T) {
	p, _, fb := newPipeline(t)

	cap := runJob(t, p, schemas.FeedbackJob{
		Kind:              "reaction",
		CommentExternalId: 99999, // not seeded
		Reaction:          "+1",
	})
	require.True(t, cap.acked)

	events, err := fb.ListForComment(context.Background(), store.CommentId("missing"))
	require.NoError(t, err)
	require.Empty(t, events)
}

func TestHumanCommentIgnored(t *testing.T) {
	p, cs, fb := newPipeline(t)
	// Seed a HUMAN comment (not a bot one).
	gid := userGithubId
	_, err := cs.Upsert(context.Background(), store.CommentUpsert{
		TenantId: tenant, RepoId: repo, PrNumber: 7,
		Source: "human", GithubId: &gid, CommentText: "hello",
		Outcome: store.OutcomePending,
	})
	require.NoError(t, err)

	runJob(t, p, schemas.FeedbackJob{
		Kind:              "reaction",
		CommentExternalId: userGithubId,
		Reaction:          "+1",
	})

	events, err := fb.ListForComment(context.Background(), cs.AllComments()[0].CommentId)
	require.NoError(t, err)
	require.Empty(t, events)
	require.Equal(t, store.OutcomePending, cs.AllComments()[0].Outcome)
}

func TestBadPayloadAcked(t *testing.T) {
	p, _, _ := newPipeline(t)
	cap := &capturingAckNack{}
	err := p.Handle(context.Background(), []byte("not-json"), cap.ctx())
	require.NoError(t, err)
	require.True(t, cap.acked)
	require.Empty(t, cap.nacked)
}

// Sanity: verify that the JSON tag names on FeedbackJob match what the
// gateway publishes. If a field is renamed, this test catches it.
func TestPayloadShape(t *testing.T) {
	body := `{"tenant_id":"t","repo_id":"r","kind":"reaction","comment_external_id":42,"reaction":"+1","author_id":"a"}`
	var job schemas.FeedbackJob
	require.NoError(t, json.Unmarshal([]byte(body), &job))
	require.Equal(t, "reaction", job.Kind)
	require.Equal(t, int64(42), job.CommentExternalId)
	require.Equal(t, "+1", job.Reaction)
}

// Guard: when GetByGithubId itself errors, the pipeline propagates the
// error (so the bus can retry) rather than silently dropping the signal.
func TestStoreErrorPropagates(t *testing.T) {
	failing := &failingCommentStore{}
	p := feedback.NewPipeline(feedback.Deps{
		Comments: failing,
		Feedback: fakes.NewFeedbackStore(),
		Clock:    clocksystem.New(),
		Obs:      obsstdout.New("test"),
	})
	payload, _ := json.Marshal(schemas.FeedbackJob{
		Kind: "reaction", CommentExternalId: 1, Reaction: "+1",
	})
	cap := &capturingAckNack{}
	err := p.Handle(context.Background(), payload, cap.ctx())
	require.Error(t, err)
	require.False(t, cap.acked)
	require.NotEmpty(t, cap.nacked)
}

type failingCommentStore struct{}

func (failingCommentStore) Upsert(context.Context, store.CommentUpsert) (store.CommentId, error) {
	return "", nil
}
func (failingCommentStore) SearchByEmbedding(context.Context, store.SearchComments) ([]store.CommentHit, error) {
	return nil, nil
}
func (failingCommentStore) UpdateOutcome(context.Context, store.CommentId, store.Outcome, store.OutcomeSignal) error {
	return nil
}
func (failingCommentStore) ListByPr(context.Context, ports.PrRef) ([]store.Comment, error) {
	return nil, nil
}
func (failingCommentStore) GetByGithubId(context.Context, int64) (store.Comment, bool, error) {
	return store.Comment{}, false, errors.New("boom")
}
