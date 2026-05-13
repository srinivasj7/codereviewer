// Package indexer indexes default-branch pushes into code_chunks
// (design §6.2). Slice 1 implements; slice 0 is a stub.
package indexer

// Pipeline is the indexer use case. Construct via NewPipeline.
type Pipeline struct{}

// NewPipeline returns an empty stub pipeline. Real implementation lands
// in slice 1 along with the parser-tree-sitter adapter.
func NewPipeline() *Pipeline { return &Pipeline{} }
