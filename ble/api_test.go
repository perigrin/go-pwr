// ABOUTME: Guards the exported surface of the Bluetooth layer against any
// ABOUTME: function that accepts raw bytes to write to the controller.

package ble

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// checkNoExportedByteWriter parses the Go files in dir and returns one
// description per exported function or method that takes a []byte parameter.
func checkNoExportedByteWriter(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("list Go files in %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	var violations []string
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			for _, field := range fn.Type.Params.List {
				arr, ok := field.Type.(*ast.ArrayType)
				if !ok || arr.Len != nil {
					continue
				}
				if elt, ok := arr.Elt.(*ast.Ident); ok && elt.Name == "byte" {
					violations = append(violations, fmt.Sprintf("%s: %s takes []byte", fset.Position(fn.Pos()), fn.Name.Name))
				}
			}
		}
	}
	return violations, nil
}

func TestNoExportedByteWriter(t *testing.T) {
	violations, err := checkNoExportedByteWriter(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range violations {
		t.Error(v)
	}
}

