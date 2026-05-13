// Package feedback captures implicit and explicit signals on bot
// comments (design §6.3). Slice 4 implements; slice 0 is a stub.
package feedback

// Pipeline is the feedback use case. Construct via NewPipeline.
type Pipeline struct{}

// NewPipeline returns an empty stub pipeline.
func NewPipeline() *Pipeline { return &Pipeline{} }
