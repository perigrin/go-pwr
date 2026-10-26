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

// writeCallNames are the names that, anywhere in a string used as a D-Bus
// method name, make the call a GATT write.
var writeCallNames = []string{"WriteValue", "AcquireWrite"}

// requestName is the one value ble may write, a constant.
const requestName = "statusRequest"

// checkWriteSites parses the non-test Go files in dir and returns one
// description per breach of the write rule. It fails closed: a file that
// does not parse, or a dir with no non-test Go files, is an error rather than
// an empty result. A dir that holds no write site, or no declaration of
// statusRequest as a byte constant of 0x20, breaches the rule too.
//
// The rule:
//   - exactly one write site: a method call named Write, WriteWithoutResponse,
//     WriteValue or AcquireWrite, or a call with an argument that is a string,
//     or strings joined with +, containing WriteValue or AcquireWrite;
//   - its value argument is exactly the literal []byte{statusRequest};
//   - statusRequest is declared once, at the top level of a file, as
//     const statusRequest byte = 0x20;
//   - the name statusRequest is declared nowhere else (no local, parameter,
//     result, range variable, field, type or function of that name) and is
//     used nowhere except in that one literal, so it is never copied,
//     appended, aliased or addressed.
//
// Build constraints are ignored, so files for every platform are checked.
// Test files are skipped because they are not part of the package.
//
// Blind spots, by design: a D-Bus method name built from pieces that are not
// literals, a write through a helper that passes bytes along or through a
// function value, another package, a call made by reflection, and a
// writeTarget literal made elsewhere in the package are not seen. Code review
// covers them, with TestNoExportedByteWriter.
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

	topLevel := map[token.Pos]bool{} // the name in a top-level declaration
	allowed := map[token.Pos]bool{}  // the use inside a write's argument
	named := map[token.Pos]bool{}    // string literals inside a recognised call
	declarations := 0
	sites := 0
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR && gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if name.Name != requestName {
						continue
					}
					declarations++
					topLevel[name.Pos()] = true
					if gen.Tok != token.CONST || !isRequestValue(vs, i) {
						report(name.Pos(), "%s is not declared as const %s byte = 0x20", requestName, requestName)
					}
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if arg, found := writeValueArgument(call, named); found {
					sites++
					if elt := requestLiteral(arg); elt != nil {
						allowed[elt.Pos()] = true
					} else {
						report(call.Pos(), "write site passes %s, want []byte{%s}", describeArgument(arg), requestName)
					}
				}
			}
			return true
		})
	}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Ident:
				if n.Name == requestName && !topLevel[n.Pos()] && !allowed[n.Pos()] {
					report(n.Pos(), "%s is declared or used outside the one write", requestName)
				}
			case *ast.BasicLit:
				// A D-Bus write name that no recognised call uses is a write
				// the guard cannot see the value of.
				if n.Kind == token.STRING && !named[n.Pos()] && namesWrite(n) {
					report(n.Pos(), "%s names a GATT write outside a call the guard can check", n.Value)
				}
			}
			return true
		})
	}

	switch {
	case declarations == 0:
		problems = append(problems, fmt.Sprintf("%s is not declared", requestName))
	case declarations > 1:
		problems = append(problems, fmt.Sprintf("%s is declared %d times", requestName, declarations))
	}
	if sites != 1 {
		problems = append(problems, fmt.Sprintf("found %d GATT write sites, want exactly 1", sites))
	}
	return problems, nil
}

// isRequestValue reports whether name i of vs is declared with type byte and
// the value 0x20.
func isRequestValue(vs *ast.ValueSpec, i int) bool {
	if typ, ok := vs.Type.(*ast.Ident); !ok || typ.Name != "byte" || i >= len(vs.Values) {
		return false
	}
	value, ok := vs.Values[i].(*ast.BasicLit)
	if !ok || value.Kind != token.INT {
		return false
	}
	n, err := strconv.ParseUint(value.Value, 0, 8)
	return err == nil && n == 0x20
}

// requestLiteral returns the statusRequest identifier in expr when expr is
// exactly []byte{statusRequest}, and nil otherwise.
func requestLiteral(expr ast.Expr) *ast.Ident {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok || len(lit.Elts) != 1 {
		return nil
	}
	arr, ok := lit.Type.(*ast.ArrayType)
	if !ok || arr.Len != nil {
		return nil
	}
	if elem, ok := arr.Elt.(*ast.Ident); !ok || elem.Name != "byte" {
		return nil
	}
	if id, ok := lit.Elts[0].(*ast.Ident); ok && id.Name == requestName {
		return id
	}
	return nil
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
		lits := writeLiterals(arg)
		if len(lits) == 0 {
			continue
		}
		for _, lit := range lits {
			named[lit.Pos()] = true
		}
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

// writeLiterals returns the string literals in expr that name a GATT write,
// looking through + and parentheses, so that iface+".WriteValue" is seen.
func writeLiterals(expr ast.Expr) []*ast.BasicLit {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING && namesWrite(e) {
			return []*ast.BasicLit{e}
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return append(writeLiterals(e.X), writeLiterals(e.Y)...)
		}
	case *ast.ParenExpr:
		return writeLiterals(e.X)
	}
	return nil
}

// namesWrite reports whether the string literal contains the name of a GATT
// write method.
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

// describeArgument says briefly what expr is, for a message.
func describeArgument(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.BasicLit:
		return e.Value
	case *ast.CompositeLit:
		return "another literal"
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
		"second-write", "second-dbus-write", "second-concatenated-write",
		"concatenated-write-other-value", "bare-name-write", "literal-argument",
		"bare-constant-argument", "two-byte-argument", "other-value", "no-write",
		"copied", "appended", "aliased", "pointer", "shadowed-local",
		"shadowed-parameter", "shadowed-range", "name-outside-call",
		"acquire-write", "other-identifier", "declared-twice", "declared-var",
		"declared-untyped", "declared-slice",
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

	for _, name := range []string{"one-write", "one-write-concatenated-name"} {
		t.Run(name+"-passes", func(t *testing.T) {
			problems, err := checkWriteSites(filepath.Join("testdata", "writesite", name))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range problems {
				t.Errorf("a copy that keeps the rule was flagged: %s", p)
			}
		})
	}

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
