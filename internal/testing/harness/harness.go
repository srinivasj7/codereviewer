// Package harness wires fakes into a full pipeline for integration-style
// tests. Each test gets a fresh Harness and asserts on the fakes after
// invoking the pipeline.
package harness

import (
	"codereviewer/internal/adapters/clocksystem"
	"codereviewer/internal/adapters/obsstdout"
	"codereviewer/internal/core/pipelines/review"
	"codereviewer/internal/ports"
	"codereviewer/internal/testing/fakes"
)

// Harness bundles the fakes and constructs pipelines on demand.
type Harness struct {
	Vcs            *fakes.Vcs
	Llm            *fakes.Llm
	Parser         *fakes.Parser
	Clock          ports.Clock
	Obs            ports.Obs
	Repos          *fakes.RepoStore
	CodeChunks     *fakes.CodeChunkStore
	Comments       *fakes.CommentStore
	Rules          *fakes.RuleStore
	PrRuns         *fakes.PrRunStore
	Feedback       *fakes.FeedbackStore
	CostCaps       *fakes.CostCapStore
	EmbeddingCache *fakes.EmbeddingCache
}

// New constructs a fresh Harness with default-configured fakes.
func New() *Harness {
	return &Harness{
		Vcs:            fakes.NewVcs(),
		Llm:            fakes.NewLlm(),
		Parser:         fakes.NewParser(),
		Clock:          clocksystem.New(),
		Obs:            obsstdout.New("test"),
		Repos:          fakes.NewRepoStore(),
		CodeChunks:     fakes.NewCodeChunkStore(),
		Comments:       fakes.NewCommentStore(),
		Rules:          fakes.NewRuleStore(),
		PrRuns:         fakes.NewPrRunStore(),
		Feedback:       fakes.NewFeedbackStore(),
		CostCaps:       fakes.NewCostCapStore(),
		EmbeddingCache: fakes.NewEmbeddingCache(),
	}
}

// ReviewPipeline returns a Pipeline wired with all the fakes.
func (h *Harness) ReviewPipeline() *review.Pipeline {
	return review.NewPipeline(review.Deps{
		Vcs:            h.Vcs,
		Llm:            h.Llm,
		Clock:          h.Clock,
		Obs:            h.Obs,
		CodeChunks:     h.CodeChunks,
		Comments:       h.Comments,
		Rules:          h.Rules,
		PrRuns:         h.PrRuns,
		CostCaps:       h.CostCaps,
		EmbeddingCache: h.EmbeddingCache,
	})
}
