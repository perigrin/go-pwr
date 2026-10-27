// ABOUTME: Guards the connect rule: Adapter.Connect is called from one place,
// ABOUTME: connectWithRetry, and a Follower connects through that.

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
//     connectWithRetry, so no caller connects once and loses a reading to the
//     aborted connects that the retry exists for;
//   - Follower.establish calls connectWithRetry, which takes the hook that
//     configures the Conn;
//   - connectWithRetry configures each Conn before it calls ready, since ready
//     opens the status link, which copies the hooks when it is opened;
//   - connectWithRetry calls dropLeftover before it calls Connect, since
//     Connect reuses a link that is up, and a reading must never send its
//     request on a link it did not make.
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
	var configureAt, readyAt, dropAt, connectAt token.Pos
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
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "configure" && fn.Name.Name == "connectWithRetry" {
					configureAt = call.Pos()
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "Connect":
					if fn.Name.Name != "connectWithRetry" {
						breaches = append(breaches, fmt.Sprintf("%s: %s calls Connect; only connectWithRetry may", fset.Position(call.Pos()), fn.Name.Name))
					} else {
						connectAt = call.Pos()
					}
				case "dropLeftover":
					if fn.Name.Name == "connectWithRetry" {
						dropAt = call.Pos()
					}
				case "connectWithRetry":
					if fn.Name.Name == "establish" {
						establishRetries = true
					}
				case "ready":
					if fn.Name.Name == "connectWithRetry" {
						readyAt = call.Pos()
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
		breaches = append(breaches, "Follower.establish does not call connectWithRetry")
	}
	if !configureAt.IsValid() || !readyAt.IsValid() || configureAt > readyAt {
		breaches = append(breaches, "connectWithRetry does not call configure before ready")
	}
	if !dropAt.IsValid() || !connectAt.IsValid() || dropAt > connectAt {
		breaches = append(breaches, "connectWithRetry does not call dropLeftover before Connect")
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
