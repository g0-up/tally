package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// actionNameFormat is the audit action naming rule: entity.verb, lower snake case on both sides.
var actionNameFormat = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// maxForwardDepth bounds how many helper calls an action name may be forwarded through.
const maxForwardDepth = 4

// Every audit action in the API source is named entity.verb. The check reads the source
// instead of hooking the recorder, so an action on a rarely taken path is checked too: each
// audit.Event literal's Action is resolved to string literals through constants, local
// variables and the parameters of helpers that forward it. An Action the check cannot resolve
// fails as well, because it could hold any name.
func TestAuditActionNamesAreEntityDotVerb(t *testing.T) {
	src, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	found, problems, err := checkActionNames(src)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d audit action names checked", len(found))
	// Guard against the scan silently finding nothing, e.g. after a directory move.
	for _, known := range []string{"tenant.create", "session.create", "attachment.upload"} {
		if !contains(found, known) {
			t.Fatalf("scan did not find the audit action %q; %d actions found under %s", known, len(found), src)
		}
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// The check itself: it follows constants, locals and forwarding helpers, and flags bad names
// as well as names it cannot resolve.
func TestActionNameCheckFlagsBadAndUnresolvableNames(t *testing.T) {
	root := t.TempDir()
	src := `package demo

import "fmt"

const created = "order.create"

type svc struct{}

func (s *svc) record(action string) error { return use(audit.Event{Action: action}) }

func (s *svc) all(kind string) {
	_ = use(audit.Event{Action: created})
	_ = use(audit.Event{Action: "Order.Delete"})
	status := "order.void"
	if kind == "x" {
		status = "order" + ".cancel"
	}
	_ = use(audit.Event{Action: status})
	_ = s.record("order.post")
	_ = s.record("orderPost")
	_ = use(audit.Event{Action: fmt.Sprintf("order.%s", kind)})
}
`
	if err := os.MkdirAll(filepath.Join(root, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "demo", "demo.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	found, problems, err := checkActionNames(root)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	wantFound := []string{"Order.Delete", "order.cancel", "order.create", "order.post", "order.void", "orderPost"}
	if strings.Join(found, ",") != strings.Join(wantFound, ",") {
		t.Errorf("found = %v, want %v", found, wantFound)
	}
	wantProblems := []string{
		`demo/demo.go:13:30: "Order.Delete" is not entity.verb`,
		`demo/demo.go:21:30: action is not a resolvable string constant`,
		`demo/demo.go:9:76: "orderPost" is not entity.verb`,
	}
	if strings.Join(problems, "\n") != strings.Join(wantProblems, "\n") {
		t.Errorf("problems =\n%s\nwant\n%s", strings.Join(problems, "\n"), strings.Join(wantProblems, "\n"))
	}
}

// checkActionNames resolves every audit action under root and returns the names found and the
// problems (bad or unresolvable names), sorted, with paths relative to root.
func checkActionNames(root string) (found, problems []string, err error) {
	pkgs, err := parseSource(root)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range pkgs {
		for _, use := range p.actionUses() {
			names, ok := p.resolve(use.expr, use.fn, 0)
			at := p.fset.Position(use.expr.Pos())
			if rel, relErr := filepath.Rel(root, at.Filename); relErr == nil {
				at.Filename = filepath.ToSlash(rel)
			}
			if !ok {
				problems = append(problems, at.String()+": action is not a resolvable string constant")
				continue
			}
			for _, n := range names {
				found = append(found, n)
				if !actionNameFormat.MatchString(n) {
					problems = append(problems, at.String()+": "+strconv.Quote(n)+" is not entity.verb")
				}
			}
		}
	}
	sort.Strings(problems)
	return found, problems, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// pkg is one parsed directory of non-test Go files.
type pkg struct {
	fset   *token.FileSet
	files  []*ast.File
	consts map[string]ast.Expr
}

// actionUse is the Action expression of one audit.Event literal and the function declaring it.
type actionUse struct {
	expr ast.Expr
	fn   *ast.FuncDecl
}

func parseSource(root string) ([]*pkg, error) {
	byDir := map[string]*pkg{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		p := byDir[dir]
		if p == nil {
			p = &pkg{fset: fset, consts: map[string]ast.Expr{}}
			byDir[dir] = p
		}
		p.files = append(p.files, f)
		for _, decl := range f.Decls {
			if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.CONST {
				for _, spec := range gd.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if i < len(vs.Values) {
							p.consts[name.Name] = vs.Values[i]
						}
					}
				}
			}
		}
		return nil
	})
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	out := make([]*pkg, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, byDir[d])
	}
	return out, err
}

// actionUses lists the Action of every audit.Event literal in the package.
func (p *pkg) actionUses() []actionUse {
	var uses []actionUse
	for _, f := range p.files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || !isAuditEvent(lit.Type) {
					return true
				}
				for _, el := range lit.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Action" {
							uses = append(uses, actionUse{expr: kv.Value, fn: fn})
						}
					}
				}
				return true
			})
		}
	}
	return uses
}

func isAuditEvent(t ast.Expr) bool {
	sel, ok := t.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Event" {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == "audit"
}

// resolve returns every string e can hold inside fn, or false when e is not built only from
// literals, constants, local assignments and forwarded parameters.
func (p *pkg) resolve(e ast.Expr, fn *ast.FuncDecl, depth int) ([]string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return nil, false
		}
		s, err := strconv.Unquote(v.Value)
		return []string{s}, err == nil
	case *ast.ParenExpr:
		return p.resolve(v.X, fn, depth)
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return nil, false
		}
		left, ok := p.resolve(v.X, fn, depth)
		if !ok {
			return nil, false
		}
		right, ok := p.resolve(v.Y, fn, depth)
		if !ok {
			return nil, false
		}
		var out []string
		for _, l := range left {
			for _, r := range right {
				out = append(out, l+r)
			}
		}
		return out, true
	case *ast.Ident:
		if idx := paramIndex(fn, v.Name); idx >= 0 {
			return p.forwarded(fn, idx, depth)
		}
		if values := localAssignments(fn, v.Name); len(values) > 0 {
			var out []string
			for _, val := range values {
				names, ok := p.resolve(val, fn, depth)
				if !ok {
					return nil, false
				}
				out = append(out, names...)
			}
			return out, true
		}
		if c, ok := p.consts[v.Name]; ok {
			return p.resolve(c, nil, depth)
		}
	}
	return nil, false
}

// forwarded resolves parameter idx of fn from every call of fn in the package.
func (p *pkg) forwarded(fn *ast.FuncDecl, idx, depth int) ([]string, bool) {
	if depth >= maxForwardDepth {
		return nil, false
	}
	var out []string
	calls, ok := 0, true
	for _, f := range p.files {
		for _, decl := range f.Decls {
			caller, isFn := decl.(*ast.FuncDecl)
			if !isFn || caller.Body == nil {
				continue
			}
			ast.Inspect(caller.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall || !invokes(call, fn) {
					return true
				}
				calls++
				if idx >= len(call.Args) {
					ok = false
					return true
				}
				names, resolved := p.resolve(call.Args[idx], caller, depth+1)
				ok = ok && resolved
				out = append(out, names...)
				return true
			})
		}
	}
	return out, ok && calls > 0
}

// invokes reports whether call invokes fn: a method by name on any receiver, or a plain function.
func invokes(call *ast.CallExpr, fn *ast.FuncDecl) bool {
	switch f := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Recv != nil && f.Sel.Name == fn.Name.Name
	case *ast.Ident:
		return fn.Recv == nil && f.Name == fn.Name.Name
	}
	return false
}

// paramIndex is the position of the named parameter of fn, or -1.
func paramIndex(fn *ast.FuncDecl, name string) int {
	if fn == nil {
		return -1
	}
	i := 0
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			i++
			continue
		}
		for _, n := range field.Names {
			if n.Name == name {
				return i
			}
			i++
		}
	}
	return -1
}

// localAssignments lists the values assigned to name anywhere in fn, closures included.
func localAssignments(fn *ast.FuncDecl, name string) []ast.Expr {
	if fn == nil || fn.Body == nil {
		return nil
	}
	var values []ast.Expr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			if len(s.Lhs) != len(s.Rhs) {
				return true
			}
			for i, lhs := range s.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
					values = append(values, s.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			for i, id := range s.Names {
				if id.Name == name && i < len(s.Values) {
					values = append(values, s.Values[i])
				}
			}
		}
		return true
	})
	return values
}
