//go:build !cgo

// Package parsertreesitter is the ParserRegistry adapter backed by
// tree-sitter. The real implementation requires CGO; this stub takes
// over when the project is built without CGO (e.g., Windows without a
// C toolchain). Workers that need actual parsing should run from the
// Docker image which always has CGO available.
package parsertreesitter

import (
	"fmt"

	"codereviewer/internal/ports"
)

// Parser is the no-CGO stub.
type Parser struct{}

// New returns a Parser stub.
func New() *Parser { return &Parser{} }

// Supports always returns false in the non-CGO build.
func (Parser) Supports(string) bool { return false }

// ParseChunks returns an error in the non-CGO build.
func (Parser) ParseChunks(_, _ string) ([]ports.ParsedChunk, error) {
	return nil, fmt.Errorf("parsertreesitter: built without CGO; rebuild with CGO_ENABLED=1 (Docker image has it)")
}
