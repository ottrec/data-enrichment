package enrich

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestMarkersRegistered checks that every amb* string constant in the
// package is in allMarkers, and nothing else is: a marker declared and left
// out of the registry would reach the consumer with no policy row.
func TestMarkersRegistered(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if !strings.HasPrefix(n.Name, "amb") || i >= len(vs.Values) {
						continue
					}
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						v, _ := strconv.Unquote(lit.Value)
						declared = append(declared, v)
					}
				}
			}
		}
	}
	slices.Sort(declared)
	registered := slices.Sorted(slices.Values(Markers()))
	if !slices.Equal(declared, registered) {
		t.Errorf("declared markers and allMarkers differ:\ndeclared   %v\nregistered %v", declared, registered)
	}
}
