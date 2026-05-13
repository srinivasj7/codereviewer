package ports

// ParserRegistry routes a file path to a language-specific parser and
// returns symbol-bounded chunks suitable for embedding. The pilot adapter
// uses tree-sitter; the testing fake splits on blank lines.
type ParserRegistry interface {
	Supports(filePath string) bool
	ParseChunks(filePath, content string) ([]ParsedChunk, error)
}

// ParsedChunk is one symbol-bounded slice of a file.
type ParsedChunk struct {
	SymbolName string
	SymbolKind string // function | class | method | interface | type | block
	StartLine  int
	EndLine    int
	Content    string
}
