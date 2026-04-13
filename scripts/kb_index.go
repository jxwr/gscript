// scripts/kb_index.go builds the L1 mechanical symbol index for the
// GScript knowledge base. It walks internal/ and gscript/ (and any other
// directories passed on the command line), parses each .go file with
// go/parser, and emits three JSON files under kb/index/:
//
//   symbols.json    — every top-level func, type, const, var with file:line
//   file_map.json   — per-file: package, LOC, public symbols, test file (if any)
//   call_graph.json — function-to-function call edges (shallow, best-effort)
//
// Run via scripts/kb_index.sh or directly: `go run scripts/kb_index.go`.
// Regeneration target: <10 seconds on the full repo.
//
// This index is NEVER read by AI during a round — it exists to answer
// mechanical questions ("where is FooBar defined?") via helper scripts,
// and to drive kb_check.sh's staleness detection against L2 cards.
//
// No external dependencies — uses only Go's standard library (go/parser,
// go/ast, encoding/json).

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Symbol is a single top-level declaration.
type Symbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"` // "func", "method", "type", "const", "var"
	Receiver  string `json:"receiver,omitempty"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Signature string `json:"signature,omitempty"` // for funcs/methods
	Doc       string `json:"doc,omitempty"`       // first line of doc
	Exported  bool   `json:"exported"`
}

// FileInfo is per-file metadata.
type FileInfo struct {
	Path      string   `json:"path"`
	Package   string   `json:"package"`
	LOC       int      `json:"loc"`
	NumSyms   int      `json:"num_syms"`
	Public    []string `json:"public"`  // names of exported top-level decls
	HasTest   bool     `json:"has_test"`
	BuildTags string   `json:"build_tags,omitempty"`
}

// CallEdge is a directed "caller calls callee" edge. Best-effort — we
// only record cross-package calls that resolve to package-qualified
// identifiers (pkg.Func), plus intra-file calls to top-level functions
// defined in the same file.
type CallEdge struct {
	Caller string `json:"caller"`
	Callee string `json:"callee"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

func main() {
	outDir := flag.String("out", "kb/index", "output directory")
	flag.Parse()

	roots := flag.Args()
	if len(roots) == 0 {
		roots = []string{"internal", "gscript", "cmd"}
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		die("mkdir %s: %v", *outDir, err)
	}

	fset := token.NewFileSet()
	var symbols []Symbol
	var files []FileInfo
	var edges []CallEdge

	// Package-qualified name of every top-level func/method for quick
	// "is this a known symbol" lookups during call-edge resolution.
	knownFuncs := make(map[string]bool)

	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// Skip archive + vendor + .git.
				name := d.Name()
				if name == "archive" || name == "vendor" || name == ".git" || name == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			// Skip files under opt/archive explicitly (belt + suspenders).
			if strings.Contains(path, "/archive/") {
				return nil
			}

			f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				// Don't fail the whole index on one bad file.
				fmt.Fprintf(os.Stderr, "parse %s: %v\n", path, err)
				return nil
			}

			pkgName := f.Name.Name
			fi := FileInfo{
				Path:    path,
				Package: pkgName,
				LOC:     countLOC(fset, f),
			}

			// Build tag constraint line (if any) — captured from the
			// first //go:build comment before the package clause.
			for _, cg := range f.Comments {
				if cg.Pos() < f.Package {
					for _, c := range cg.List {
						if strings.HasPrefix(c.Text, "//go:build ") {
							fi.BuildTags = strings.TrimPrefix(c.Text, "//go:build ")
						}
					}
				}
			}

			for _, decl := range f.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					s := funcSymbol(fset, path, pkgName, d)
					symbols = append(symbols, s)
					knownFuncs[pkgName+"."+s.Name] = true
					if s.Exported {
						fi.Public = append(fi.Public, s.Name)
					}
					fi.NumSyms++
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						s, ok := genSymbol(fset, path, spec, d.Tok.String())
						if !ok {
							continue
						}
						symbols = append(symbols, s)
						if s.Exported {
							fi.Public = append(fi.Public, s.Name)
						}
						fi.NumSyms++
					}
				}
			}

			// Quick test-file presence check: foo.go → foo_test.go.
			if strings.HasSuffix(path, "_test.go") {
				fi.HasTest = true
			} else {
				testPath := strings.TrimSuffix(path, ".go") + "_test.go"
				if _, err := os.Stat(testPath); err == nil {
					fi.HasTest = true
				}
			}

			files = append(files, fi)

			// Second pass: call edges. We collect intra-file calls to
			// top-level identifiers (callee name matches an ast.FuncDecl
			// in the same file) and package-qualified calls of the form
			// ident.Method — the latter are kept even if the callee
			// resolution isn't verified.
			collectEdges(fset, path, f, &edges)

			return nil
		})
		if err != nil {
			die("walk %s: %v", root, err)
		}
	}

	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].File != symbols[j].File {
			return symbols[i].File < symbols[j].File
		}
		return symbols[i].Line < symbols[j].Line
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	writeJSON(filepath.Join(*outDir, "symbols.json"), symbols)
	writeJSON(filepath.Join(*outDir, "file_map.json"), files)
	writeJSON(filepath.Join(*outDir, "call_graph.json"), edges)

	// Short stdout summary so the kb_index.sh wrapper can log it.
	fmt.Printf("kb/index: %d files, %d symbols, %d call edges (roots: %v)\n",
		len(files), len(symbols), len(edges), roots)
}

func funcSymbol(fset *token.FileSet, path, pkgName string, d *ast.FuncDecl) Symbol {
	name := d.Name.Name
	kind := "func"
	var recv string
	if d.Recv != nil && len(d.Recv.List) > 0 {
		kind = "method"
		recv = typeString(d.Recv.List[0].Type)
	}
	pos := fset.Position(d.Pos())
	sig := "func "
	if kind == "method" {
		sig += "(" + recv + ") "
	}
	sig += name + "("
	if d.Type.Params != nil {
		var parts []string
		for _, p := range d.Type.Params.List {
			parts = append(parts, typeString(p.Type))
		}
		sig += strings.Join(parts, ", ")
	}
	sig += ")"
	if d.Type.Results != nil && len(d.Type.Results.List) > 0 {
		var parts []string
		for _, r := range d.Type.Results.List {
			parts = append(parts, typeString(r.Type))
		}
		if len(parts) == 1 {
			sig += " " + parts[0]
		} else {
			sig += " (" + strings.Join(parts, ", ") + ")"
		}
	}
	return Symbol{
		Name:      name,
		Kind:      kind,
		Receiver:  recv,
		File:      path,
		Line:      pos.Line,
		Signature: sig,
		Doc:       firstDocLine(d.Doc),
		Exported:  ast.IsExported(name),
	}
}

func genSymbol(fset *token.FileSet, path string, spec ast.Spec, tok string) (Symbol, bool) {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		pos := fset.Position(s.Pos())
		return Symbol{
			Name:     s.Name.Name,
			Kind:     "type",
			File:     path,
			Line:     pos.Line,
			Exported: ast.IsExported(s.Name.Name),
		}, true
	case *ast.ValueSpec:
		if len(s.Names) == 0 {
			return Symbol{}, false
		}
		// Emit one symbol per name.
		pos := fset.Position(s.Pos())
		kind := "var"
		if tok == "const" {
			kind = "const"
		}
		return Symbol{
			Name:     s.Names[0].Name,
			Kind:     kind,
			File:     path,
			Line:     pos.Line,
			Exported: ast.IsExported(s.Names[0].Name),
		}, true
	}
	return Symbol{}, false
}

func typeString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.ArrayType:
		return "[]" + typeString(t.Elt)
	case *ast.MapType:
		return "map[" + typeString(t.Key) + "]" + typeString(t.Value)
	case *ast.Ellipsis:
		return "..." + typeString(t.Elt)
	case *ast.FuncType:
		return "func"
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.ChanType:
		return "chan " + typeString(t.Value)
	case *ast.StructType:
		return "struct"
	}
	return "?"
}

func firstDocLine(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	for _, c := range cg.List {
		line := strings.TrimPrefix(c.Text, "//")
		line = strings.TrimPrefix(line, "/*")
		line = strings.TrimSuffix(line, "*/")
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func countLOC(fset *token.FileSet, f *ast.File) int {
	// Use the file set to get end-of-file position in lines.
	return fset.Position(f.End()).Line
}

func collectEdges(fset *token.FileSet, path string, f *ast.File, out *[]CallEdge) {
	// Pre-collect local top-level functions for intra-file resolution.
	local := make(map[string]bool)
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
			local[fn.Name.Name] = true
		}
	}

	var currentCaller string
	ast.Inspect(f, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok {
			name := fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				name = typeString(fn.Recv.List[0].Type) + "." + name
			}
			currentCaller = f.Name.Name + "." + name
			return true
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var calleeName string
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if local[fn.Name] {
				calleeName = f.Name.Name + "." + fn.Name
			} else {
				return true
			}
		case *ast.SelectorExpr:
			if id, ok := fn.X.(*ast.Ident); ok {
				calleeName = id.Name + "." + fn.Sel.Name
			} else {
				return true
			}
		default:
			return true
		}
		pos := fset.Position(call.Pos())
		*out = append(*out, CallEdge{
			Caller: currentCaller,
			Callee: calleeName,
			File:   path,
			Line:   pos.Line,
		})
		return true
	})
}

func writeJSON(path string, v any) {
	f, err := os.Create(path)
	if err != nil {
		die("create %s: %v", path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		die("encode %s: %v", path, err)
	}
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "kb_index: "+format+"\n", a...)
	os.Exit(1)
}
