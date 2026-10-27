// ABOUTME: Guards the connect rule: Adapter.Connect is called from one place,
// ABOUTME: ConnectWithRetry, and a Follower connects through that.

package ble

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// checkConnectSites parses the non-test Go files in dir and returns one
// description per breach of the connect rule:
//   - a call to a method named Connect is made only inside the function
//     ConnectWithRetry, so no caller connects once and loses a reading to the
//     aborted connects that the retry exists for;
//   - Follower.establish calls ConnectWithRetry.
//
// It fails closed: a file that does not parse, or a dir with no non-test Go
// files, is an error. Build constraints are ignored. Blind spots: a call
// through a function value or an interface, and one made by reflection.
func checkConnectSites(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("list Go files in %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	var breaches []string
	var files int
	establishRetries := false
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		files++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "Connect":
					if fn.Name.Name != "ConnectWithRetry" {
						breaches = append(breaches, fmt.Sprintf("%s: %s calls Connect; only ConnectWithRetry may", fset.Position(call.Pos()), fn.Name.Name))
					}
				case "ConnectWithRetry":
					if fn.Name.Name == "establish" {
						establishRetries = true
					}
				}
				return true
			})
		}
	}
	if files == 0 {
		return nil, fmt.Errorf("no non-test Go files in %s", dir)
	}
	if !establishRetries {
		breaches = append(breaches, "Follower.establish does not call ConnectWithRetry")
	}
	return breaches, nil
}

func TestConnectOnlyThroughConnectWithRetry(t *testing.T) {
	breaches, err := checkConnectSites(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range breaches {
		t.Error(b)
	}
}
