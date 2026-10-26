// ABOUTME: Guards the exported surface of the Bluetooth layer against any
// ABOUTME: function that accepts raw bytes to write to the controller.

package ble

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkNoExportedByteWriter parses the non-test Go files in dir and returns
// one description per exported function, method or interface method that
// takes raw bytes to write. It fails closed: a file that does not parse, or a
// dir with no non-test Go files, is an error rather than an empty result.
//
// Build constraints are ignored, so files for every platform are checked.
// Test files are skipped because they are not part of the exported API.
//
// A parameter carries raw bytes when its type is byte or uint8, or an
// array, slice, pointer or variadic of such a type, or a type name declared
// in dir (defined or alias, exported or not) that resolves to one. Callbacks,
// struct fields, generic type parameters and results are not inspected.
func checkNoExportedByteWriter(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("list Go files in %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no non-test Go files in %s", dir)
	}

	types := map[string]ast.Expr{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts := spec.(*ast.TypeSpec)
				types[ts.Name.Name] = ts.Type
			}
		}
	}

	var violations []string
	check := func(name string, params *ast.FieldList) {
		if params == nil {
			return
		}
		for _, field := range params.List {
			if carriesBytes(field.Type, types, map[string]bool{}) {
				violations = append(violations, fmt.Sprintf("%s: %s takes raw bytes", fset.Position(field.Pos()), name))
			}
		}
	}
	for _, file := range files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Name.IsExported() {
					check(d.Name.Name, d.Type.Params)
				}
			case *ast.GenDecl:
				if d.Tok != token.TYPE {
					continue
				}
				for _, spec := range d.Specs {
					ts := spec.(*ast.TypeSpec)
					iface, ok := ts.Type.(*ast.InterfaceType)
					if !ok || !ts.Name.IsExported() {
						continue
					}
					for _, m := range iface.Methods.List {
						fn, ok := m.Type.(*ast.FuncType)
						if !ok {
							continue
						}
						for _, name := range m.Names {
							if name.IsExported() {
								check(ts.Name.Name+"."+name.Name, fn.Params)
							}
						}
					}
				}
			}
		}
	}
	return violations, nil
}

// carriesBytes reports whether expr is, or is built from, byte or uint8.
// seen guards against type declarations that refer to themselves.
func carriesBytes(expr ast.Expr, types map[string]ast.Expr, seen map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		if e.Name == "byte" || e.Name == "uint8" {
			return true
		}
		underlying, ok := types[e.Name]
		if !ok || seen[e.Name] {
			return false
		}
		seen[e.Name] = true
		return carriesBytes(underlying, types, seen)
	case *ast.ArrayType:
		return carriesBytes(e.Elt, types, seen)
	case *ast.Ellipsis:
		return carriesBytes(e.Elt, types, seen)
	case *ast.StarExpr:
		return carriesBytes(e.X, types, seen)
	case *ast.ParenExpr:
		return carriesBytes(e.X, types, seen)
	}
	return false
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

func TestNoExportedByteWriterCatchesVariants(t *testing.T) {
	for _, name := range []string{
		"slice", "method", "variadic", "defined", "alias", "chained",
		"linuxtag", "bare", "pointer", "array", "nested", "iface",
	} {
		t.Run(name, func(t *testing.T) {
			violations, err := checkNoExportedByteWriter(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			if len(violations) == 0 {
				t.Errorf("testdata/%s: guard reported no violation, want at least one", name)
			}
		})
	}
}

func TestNoExportedByteWriterFailsClosed(t *testing.T) {
	unparsable := t.TempDir()
	if err := os.WriteFile(filepath.Join(unparsable, "broken.go"), []byte("package ble\n\nfunc Write(b []byte {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "README.txt"), []byte("no Go files here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{
		"unparsable":     unparsable,
		"empty":          empty,
		"only-tests":     filepath.Join("testdata", "onlytests"),
		"does-not-exist": filepath.Join("testdata", "does-not-exist"),
	} {
		t.Run(name, func(t *testing.T) {
			violations, err := checkNoExportedByteWriter(dir)
			if err == nil {
				t.Errorf("%s: guard returned no error (violations %v), want an error", dir, violations)
			}
		})
	}
}

func TestNoExportedByteWriterIgnoresUnexported(t *testing.T) {
	violations, err := checkNoExportedByteWriter(filepath.Join("testdata", "unexported"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range violations {
		t.Errorf("unexported byte helper flagged: %s", v)
	}
}
