// ABOUTME: Guards the write rule: ble has exactly one GATT value-write call
// ABOUTME: site, and it writes the constant statusRequest, the byte 0x20.

package ble

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeMethodNames are the method names that write a GATT value, whichever
// library or interface they belong to. The value is the first argument.
var writeMethodNames = map[string]bool{
	"Write":                true,
	"WriteWithoutResponse": true,
	"WriteValue":           true,
	"AcquireWrite":         true,
}

// writeCallNames are the D-Bus method names, as they appear in a string, that
// write a GATT value.
var writeCallNames = []string{
	"org.bluez.GattCharacteristic1.WriteValue",
	"org.bluez.GattCharacteristic1.AcquireWrite",
	"GattCharacteristic1.WriteValue",
	"GattCharacteristic1.AcquireWrite",
}

// requestVariable is the one value ble may write.
const requestVariable = "statusRequest"

// checkWriteSites parses the non-test Go files in dir and returns one
// description per breach of the write rule. It fails closed: a file that
// does not parse, or a dir with no non-test Go files, is an error rather than
// an empty result. A dir that holds no write site, or no declaration of
// statusRequest as []byte{0x20}, breaches the rule too.
//
// The rule: exactly one write site, which is a method call named Write,
// WriteWithoutResponse, WriteValue or AcquireWrite, or a call that has one of
// the D-Bus names of those as a string literal argument; its value argument
// is the identifier statusRequest; and statusRequest is declared once as
// []byte{0x20} and never assigned to again.
//
// Build constraints are ignored, so files for every platform are checked.
// Test files are skipped because they are not part of the package.
//
// Blind spots, by design: a D-Bus method name built at run time or held in a
// variable, a write through a function value, and a call made by reflection
// are not seen. Code review covers them, with TestNoExportedByteWriter.
func checkWriteSites(dir string) ([]string, error) {
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

	var problems []string
	report := func(pos token.Pos, format string, args ...any) {
		problems = append(problems, fmt.Sprintf("%s: %s", fset.Position(pos), fmt.Sprintf(format, args...)))
	}

	declarations := 0
	sites := 0
	named := map[token.Pos]bool{} // string literals inside a recognised call
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR && gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if name.Name != requestVariable {
						continue
					}
					declarations++
					if gen.Tok != token.VAR || i >= len(vs.Values) || !isStatusRequestValue(vs.Values[i]) {
						report(name.Pos(), "%s is not declared as var %s = []byte{0x20}", requestVariable, requestVariable)
					}
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if arg, found := writeValueArgument(n, named); found {
					sites++
					if id, ok := arg.(*ast.Ident); !ok || id.Name != requestVariable {
						report(n.Pos(), "write site passes %s, want %s", describeArgument(arg), requestVariable)
					}
				}
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if assignsRequest(lhs) {
						report(n.Pos(), "%s is assigned to", requestVariable)
					}
				}
			case *ast.IncDecStmt:
				if assignsRequest(n.X) {
					report(n.Pos(), "%s is changed", requestVariable)
				}
			}
			return true
		})
	}
	// A D-Bus write name that no recognised call uses is a write the guard
	// cannot see the value of.
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && !named[lit.Pos()] && namesWrite(lit) {
				report(lit.Pos(), "%s names a GATT write outside a call the guard can check", lit.Value)
			}
			return true
		})
	}

	switch {
	case declarations == 0:
		problems = append(problems, fmt.Sprintf("%s is not declared", requestVariable))
	case declarations > 1:
		problems = append(problems, fmt.Sprintf("%s is declared %d times", requestVariable, declarations))
	}
	if sites != 1 {
		problems = append(problems, fmt.Sprintf("found %d GATT write sites, want exactly 1", sites))
	}
	return problems, nil
}

// isStatusRequestValue reports whether expr is exactly []byte{0x20}.
func isStatusRequestValue(expr ast.Expr) bool {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok || len(lit.Elts) != 1 {
		return false
	}
	arr, ok := lit.Type.(*ast.ArrayType)
	if !ok || arr.Len != nil {
		return false
	}
	if elem, ok := arr.Elt.(*ast.Ident); !ok || elem.Name != "byte" {
		return false
	}
	value, ok := lit.Elts[0].(*ast.BasicLit)
	if !ok || value.Kind != token.INT {
		return false
	}
	n, err := strconv.ParseUint(value.Value, 0, 8)
	return err == nil && n == 0x20
}

// writeValueArgument returns the value argument of call when call is a GATT
// write site, and records in named the string literals that made it one.
func writeValueArgument(call *ast.CallExpr, named map[token.Pos]bool) (ast.Expr, bool) {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && writeMethodNames[sel.Sel.Name] {
		if len(call.Args) == 0 {
			return &ast.BadExpr{From: call.Pos(), To: call.End()}, true
		}
		return call.Args[0], true
	}
	for i, arg := range call.Args {
		lit, ok := arg.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || !namesWrite(lit) {
			continue
		}
		named[lit.Pos()] = true
		// A D-Bus call is (method, flags, args...), after a context for the
		// context-aware forms, and Go also takes a channel before the args.
		// The value is the first argument after the flags in every form the
		// package uses, so take the one two places on.
		if i+2 >= len(call.Args) {
			return &ast.BadExpr{From: call.Pos(), To: call.End()}, true
		}
		return call.Args[i+2], true
	}
	return nil, false
}

// namesWrite reports whether the string literal names a GATT write method.
func namesWrite(lit *ast.BasicLit) bool {
	text, err := strconv.Unquote(lit.Value)
	if err != nil {
		text = lit.Value
	}
	for _, name := range writeCallNames {
		if strings.Contains(text, name) {
			return true
		}
	}
	return false
}

// assignsRequest reports whether expr is statusRequest or one of its
// elements, as the left side of an assignment.
func assignsRequest(expr ast.Expr) bool {
	for {
		switch e := expr.(type) {
		case *ast.Ident:
			return e.Name == requestVariable
		case *ast.IndexExpr:
			expr = e.X
		case *ast.SliceExpr:
			expr = e.X
		case *ast.ParenExpr:
			expr = e.X
		default:
			return false
		}
	}
}

// describeArgument says briefly what expr is, for a message.
func describeArgument(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.BasicLit:
		return e.Value
	case *ast.CompositeLit:
		return "a literal"
	case *ast.BadExpr:
		return "nothing"
	}
	return fmt.Sprintf("a %T", expr)
}

func TestSingleStatusWriteCallSite(t *testing.T) {
	problems, err := checkWriteSites(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// TestSingleStatusWriteCallSiteFailsClosed runs the guard on copies of ble
// that break the rule in each way it checks, and on one copy that keeps it,
// so the guard is shown to fail when it should and to pass when it should.
func TestSingleStatusWriteCallSiteFailsClosed(t *testing.T) {
	for _, name := range []string{
		"second-write", "second-dbus-write", "literal-argument", "other-value",
		"no-write", "element-assigned", "name-outside-call", "acquire-write",
		"other-identifier", "declared-twice", "declared-const",
	} {
		t.Run(name, func(t *testing.T) {
			problems, err := checkWriteSites(filepath.Join("testdata", "writesite", name))
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) == 0 {
				t.Errorf("testdata/writesite/%s: guard reported no problem, want at least one", name)
			}
		})
	}

	t.Run("one-write-passes", func(t *testing.T) {
		problems, err := checkWriteSites(filepath.Join("testdata", "writesite", "one-write"))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range problems {
			t.Errorf("a copy that keeps the rule was flagged: %s", p)
		}
	})

	t.Run("errors", func(t *testing.T) {
		unparsable := t.TempDir()
		if err := os.WriteFile(filepath.Join(unparsable, "broken.go"), []byte("package ble\n\nfunc f( {\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		empty := t.TempDir()
		for name, dir := range map[string]string{
			"unparsable":     unparsable,
			"empty":          empty,
			"only-tests":     filepath.Join("testdata", "onlytests"),
			"does-not-exist": filepath.Join("testdata", "writesite", "does-not-exist"),
		} {
			problems, err := checkWriteSites(dir)
			if err == nil {
				t.Errorf("%s: guard returned no error (problems %v), want an error", name, problems)
			}
		}
	})
}
