//go:build cgo

package parsertreesitter

import (
	"context"
	"fmt"
	"path/filepath"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"

	"codereviewer/internal/ports"
)

// Parser is the tree-sitter implementation of ports.ParserRegistry.
type Parser struct{}

// New returns a Parser.
func New() *Parser { return &Parser{} }

// Supports returns true for ts/tsx/js/jsx/json.
func (Parser) Supports(path string) bool {
	switch filepath.Ext(path) {
	case ".ts", ".tsx", ".js", ".jsx", ".json":
		return true
	}
	return false
}

// ParseChunks splits content into symbol-bounded chunks at the
// top-level declarations: function_declaration, class_declaration,
// method_definition, interface_declaration, type_alias_declaration.
// JSON files become a single chunk (no useful sub-structure).
func (Parser) ParseChunks(filePath, content string) ([]ports.ParsedChunk, error) {
	ext := filepath.Ext(filePath)
	if ext == ".json" {
		return []ports.ParsedChunk{{
			SymbolName: filepath.Base(filePath),
			SymbolKind: "file",
			StartLine:  1,
			EndLine:    lineCount(content),
			Content:    content,
		}}, nil
	}

	lang := languageFor(ext)
	if lang == nil {
		return nil, fmt.Errorf("unsupported extension %q", ext)
	}

	p := sitter.NewParser()
	p.SetLanguage(lang)
	tree, err := p.ParseCtx(context.Background(), nil, []byte(content))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}
	defer tree.Close()

	root := tree.RootNode()
	chunks := make([]ports.ParsedChunk, 0, root.NamedChildCount())
	for i := 0; uint32(i) < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		chunk, ok := nodeToChunk(n, content)
		if ok {
			chunks = append(chunks, chunk)
		}
	}
	return chunks, nil
}

func nodeToChunk(n *sitter.Node, content string) (ports.ParsedChunk, bool) {
	kind, name := classify(n, content)
	if kind == "" {
		return ports.ParsedChunk{}, false
	}
	return ports.ParsedChunk{
		SymbolName: name,
		SymbolKind: kind,
		StartLine:  int(n.StartPoint().Row) + 1,
		EndLine:    int(n.EndPoint().Row) + 1,
		Content:    n.Content([]byte(content)),
	}, true
}

func classify(n *sitter.Node, content string) (kind, name string) {
	// export_statement wraps a declaration; dig in one level.
	if n.Type() == "export_statement" && n.NamedChildCount() > 0 {
		return classify(n.NamedChild(0), content)
	}
	switch n.Type() {
	case "function_declaration":
		return "function", childText(n, "name", content)
	case "class_declaration":
		return "class", childText(n, "name", content)
	case "method_definition":
		return "method", childText(n, "name", content)
	case "interface_declaration":
		return "interface", childText(n, "name", content)
	case "type_alias_declaration":
		return "type", childText(n, "name", content)
	case "lexical_declaration", "variable_declaration":
		// Top-level const/let/var — emit only if it binds a function/arrow.
		if hasArrowFunction(n, content) {
			return "function", firstIdentifier(n, content)
		}
	}
	return "", ""
}

func childText(n *sitter.Node, fieldName, content string) string {
	c := n.ChildByFieldName(fieldName)
	if c == nil {
		return ""
	}
	return c.Content([]byte(content))
}

func hasArrowFunction(n *sitter.Node, _ string) bool {
	for i := 0; uint32(i) < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Type() == "variable_declarator" {
			for j := 0; uint32(j) < c.NamedChildCount(); j++ {
				gc := c.NamedChild(j)
				if gc.Type() == "arrow_function" || gc.Type() == "function" {
					return true
				}
			}
		}
	}
	return false
}

func firstIdentifier(n *sitter.Node, content string) string {
	for i := 0; uint32(i) < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Type() == "variable_declarator" {
			id := c.ChildByFieldName("name")
			if id != nil {
				return id.Content([]byte(content))
			}
		}
	}
	return ""
}

func languageFor(ext string) *sitter.Language {
	switch ext {
	case ".ts":
		return typescript.GetLanguage()
	case ".tsx":
		return tsx.GetLanguage()
	case ".js", ".jsx":
		return javascript.GetLanguage()
	}
	return nil
}

func lineCount(s string) int {
	n := 1
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
		}
	}
	return n
}
