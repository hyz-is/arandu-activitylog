package unit_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The audit reads the package's own source, because `aru doctor` reads the
// application it runs in and never opens an installed package: whatever this
// package must prove about itself, it proves here or nowhere. It reads syntax,
// so a green run means no such thing was found written down, not that none
// exists.

type sourceFile struct {
	path string
	file *ast.File
	text string
}

func packageRoot(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
}

// productionFiles are the package's Go files, without tests and the
// generated query.
func productionFiles(t *testing.T) []sourceFile {
	t.Helper()
	root := packageRoot(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var out []sourceFile
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(root, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, path, raw, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, sourceFile{path: name, file: file, text: string(raw)})
	}
	if len(out) == 0 {
		t.Fatal("no Go file was found, so everything below would pass by having nothing to read")
	}
	return out
}

func receiverType(decl *ast.FuncDecl) string {
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		return ""
	}
	switch expr := decl.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if ident, ok := expr.X.(*ast.Ident); ok {
			return ident.Name
		}
	case *ast.Ident:
		return expr.Name
	}
	return ""
}

func calledName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name
	case *ast.Ident:
		return fun.Name
	}
	return ""
}

func firstCall(body *ast.BlockStmt, name string) token.Pos {
	found := token.NoPos
	ast.Inspect(body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && calledName(call) == name && (found == token.NoPos || call.Pos() < found) {
			found = call.Pos()
		}
		return true
	})
	return found
}

// Every exported method of the service a person reaches asks the policy
// before it builds a query: a method that read first would answer whoever
// asked.
func TestEveryServiceMethodAuthorizesBeforeTheModel(t *testing.T) {
	checked := 0
	for _, source := range productionFiles(t) {
		for _, decl := range source.file.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok || function.Body == nil || receiverType(function) != "ActivityService" || !function.Name.IsExported() {
				continue
			}
			checked++
			authorize := firstCall(function.Body, "Authorize")
			if authorize == token.NoPos {
				t.Errorf("ActivityService.%s never calls security.Authorize", function.Name.Name)
				continue
			}
			for _, entry := range []string{"Activities", "filtered", "clean"} {
				if reach := firstCall(function.Body, entry); reach != token.NoPos && reach < authorize {
					t.Errorf("ActivityService.%s reaches %s before it authorizes", function.Name.Name, entry)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no exported ActivityService method was found, so this test proved nothing")
	}
}

// No tenant is read out of the request: the read routes take their subject
// from the session, and every write takes its tenant from the Grant.
func TestNoTenantIsReadOutOfTheRequest(t *testing.T) {
	for _, source := range productionFiles(t) {
		ast.Inspect(source.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			switch calledName(call) {
			case "Query", "Param", "Input", "Get", "FormValue":
			default:
				return true
			}
			if literal, ok := call.Args[0].(*ast.BasicLit); ok && strings.Contains(strings.ToLower(literal.Value), "tenant") {
				t.Errorf("%s reads %s out of the request", source.path, literal.Value)
			}
			return true
		})
		if strings.Contains(source.text, "TenantID =") {
			t.Errorf("%s writes a tenant by hand; the model stamps it from the Grant", source.path)
		}
	}
}

// The manifest is what the code does: no network, no file, no other program,
// and the table it owns.
func TestTheDeclaredCapabilitiesAreWhatTheCodeDoes(t *testing.T) {
	forbidden := map[string]string{
		"WriteFile": "filesystem", "Create": "filesystem", "OpenFile": "filesystem", "MkdirAll": "filesystem", "ReadFile": "filesystem",
		"Command": "exec", "StartProcess": "exec",
		"Dial": "network", "DialContext": "network", "Do": "network", "Post": "network", "PostForm": "network", "Head": "network",
	}
	migrations := false
	for _, source := range productionFiles(t) {
		ast.Inspect(source.file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.CallExpr:
				selector, ok := node.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}
				if capability, bad := forbidden[selector.Sel.Name]; bad && (pkg.Name == "os" || pkg.Name == "exec" || pkg.Name == "net" || pkg.Name == "http") {
					t.Errorf("%s calls %s.%s, and arandu.mod.toml declares %s = false", source.path, pkg.Name, selector.Sel.Name, capability)
				}
			case *ast.FuncDecl:
				if node.Name.Name == "Migrations" && node.Recv != nil {
					migrations = true
				}
			}
			return true
		})
	}
	manifest, err := os.ReadFile(filepath.Join(packageRoot(t), "arandu.mod.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"network = false", "filesystem = false", "exec = false"} {
		if !strings.Contains(string(manifest), line) {
			t.Errorf("arandu.mod.toml does not declare %q", line)
		}
	}
	if migrations != strings.Contains(string(manifest), "migrations = true") {
		t.Errorf("the code declares migrations: %v, and the manifest disagrees", migrations)
	}
}
