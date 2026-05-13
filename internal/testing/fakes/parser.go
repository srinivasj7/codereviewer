package fakes

import (
	"fmt"
	"path/filepath"
	"strings"

	"codereviewer/internal/ports"
)

// Parser is a fake ParserRegistry that splits content on blank lines.
// Supports the same extensions as the production tree-sitter adapter
// will (ts/tsx/js/jsx/json) so callers can swap implementations.
type Parser struct{}

// NewParser returns a Parser.
func NewParser() *Parser { return &Parser{} }

// Supports returns true for ts/tsx/js/jsx/json files.
func (Parser) Supports(path string) bool {
	switch filepath.Ext(path) {
	case ".ts", ".tsx", ".js", ".jsx", ".json":
		return true
	}
	return false
}

// ParseChunks splits content on blank-line boundaries. SymbolName is
// synthetic; real parser uses tree-sitter AST nodes.
func (Parser) ParseChunks(_, content string) ([]ports.ParsedChunk, error) {
	blocks := strings.Split(content, "\n\n")
	var chunks []ports.ParsedChunk
	line := 1
	for i, b := range blocks {
		if strings.TrimSpace(b) == "" {
			line += strings.Count(b, "\n") + 2
			continue
		}
		end := line + strings.Count(b, "\n")
		chunks = append(chunks, ports.ParsedChunk{
			SymbolName: fmt.Sprintf("block-%d", i),
			SymbolKind: "block",
			StartLine:  line,
			EndLine:    end,
			Content:    b,
		})
		line = end + 2
	}
	return chunks, nil
}
