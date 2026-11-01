package gqlcli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// fragmentDecl is one parsed entry of the "fragments" section in .gqlcli.json.
type fragmentDecl struct {
	source  string
	spreads []string
}

// indexFragmentDeclarations parses each declared fragment once, so expansion
// can follow fragment-to-fragment spreads without reparsing the same text.
func indexFragmentDeclarations(declarations map[string]string) (map[string]fragmentDecl, error) {
	index := make(map[string]fragmentDecl, len(declarations))
	keys := make([]string, 0, len(declarations))
	for key := range declarations {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		source := strings.TrimSpace(declarations[key])
		if source == "" {
			return nil, fmt.Errorf("fragment %q in .gqlcli.json is empty", key)
		}
		doc, err := parser.ParseQuery(&ast.Source{Name: "fragments:" + key, Input: source})
		if err != nil {
			return nil, fmt.Errorf("fragment %q in .gqlcli.json: %w", key, err)
		}
		if len(doc.Operations) != 0 || len(doc.Fragments) != 1 {
			return nil, fmt.Errorf("fragment %q in .gqlcli.json must contain exactly one fragment definition", key)
		}
		def := doc.Fragments[0]
		if def.Name != key {
			return nil, fmt.Errorf("fragment key %q in .gqlcli.json declares fragment %q; key and declaration name must match", key, def.Name)
		}
		index[key] = fragmentDecl{source: source, spreads: collectFragmentSpreads(def.SelectionSet)}
	}
	return index, nil
}

// collectFragmentSpreads returns every fragment name spread anywhere in set.
func collectFragmentSpreads(set ast.SelectionSet) []string {
	var spreads []string
	var walk func(ast.SelectionSet)
	walk = func(ss ast.SelectionSet) {
		for _, selection := range ss {
			switch s := selection.(type) {
			case *ast.Field:
				walk(s.SelectionSet)
			case *ast.InlineFragment:
				walk(s.SelectionSet)
			case *ast.FragmentSpread:
				spreads = append(spreads, s.Name)
			}
		}
	}
	walk(set)
	return spreads
}

// ExpandFragments appends the declarations a document needs for fragment
// spreads it does not define itself.
//
// Every spread that is missing from the document is looked up in declarations
// (name -> fragment definition source) and appended, transitively, so a
// declared fragment may spread another declared fragment. Fragments already
// defined in the document are left alone, and declarations a document does not
// use are never appended, so documents that need nothing are returned
// unchanged. A missing declaration is an error rather than a pass-through: the
// server would reject the document anyway, and the local message names the
// fragment and where to declare it.
func ExpandFragments(document string, declarations map[string]string) (string, error) {
	if len(declarations) == 0 || strings.TrimSpace(document) == "" {
		return document, nil
	}

	index, err := indexFragmentDeclarations(declarations)
	if err != nil {
		return "", err
	}

	doc, err := parser.ParseQuery(&ast.Source{Name: "operation", Input: document})
	if err != nil {
		return "", fmt.Errorf("parsing document for fragment expansion: %w", err)
	}

	defined := make(map[string]bool, len(doc.Fragments))
	var pending []string
	for _, fragment := range doc.Fragments {
		defined[fragment.Name] = true
		pending = append(pending, collectFragmentSpreads(fragment.SelectionSet)...)
	}
	for _, op := range doc.Operations {
		pending = append(pending, collectFragmentSpreads(op.SelectionSet)...)
	}

	var added []string
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		if defined[name] {
			continue
		}
		decl, ok := index[name]
		if !ok {
			return "", fmt.Errorf("fragment %q is used in the document but is neither defined there nor declared under \"fragments\" in .gqlcli.json", name)
		}
		defined[name] = true
		added = append(added, decl.source)
		pending = append(pending, decl.spreads...)
	}
	if len(added) == 0 {
		return document, nil
	}

	return strings.TrimRight(document, "\n") + "\n\n" + strings.Join(added, "\n\n"), nil
}
