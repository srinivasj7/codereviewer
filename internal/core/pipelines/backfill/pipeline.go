// Package backfill ingests historical PR comments for retrieval
// (design §6.4). Slice 3 implements; slice 0 is a stub.
package backfill

// Pipeline is the backfill use case. Construct via NewPipeline.
type Pipeline struct{}

// NewPipeline returns an empty stub pipeline.
func NewPipeline() *Pipeline { return &Pipeline{} }
