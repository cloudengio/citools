// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

// Command astest generates *testing.T wrapper functions for functions that
// accept a TestingT interface and carry the //cicd:astest marker. This lets
// test-helper packages expose reusable test logic (callable with any TestingT
// implementation) while also making that logic directly runnable by `go test`.
//
// For every function of the form
//
//	func TestFoo(t SomeInterfaceType) { //cicd:astest
//	    …
//	}
//
// (with the marker in the preceding doc-comment), astest emits:
//
//	func TestFoo(t *testing.T) { pkg.TestFoo(t) }
//
// The output file may live in the same directory as the source package
// (generating an external _test package) or in a different directory (the
// package name is inferred from existing files there, falling back to the
// directory basename). The source package is always imported.
//
// The output is processed by goimports, so packages referenced by --preamble
// or --import are added to (or removed from) the import block automatically.
//
// Usage:
//
//	astest [flags] <package-dir-or-import-path> <output-file>
//
// Flags:
//
//	--pkg-path            treat the first argument as an import path rather than
//	                      a directory; the directory is resolved via go list
//	--internal_test       use an internal test package (package <pkg>);
//	                      default is the external form (package <pkg>_test)
//	--match <regexp>      only generate wrappers for marked functions whose
//	                      names match the regular expression (default: all)
//	--preamble <code>     Go statements inserted at the top of every generated
//	                      function body; use \n to separate multiple statements
//	--import <spec>       extra import added to the generated file; may be
//	                      repeated. Accepts bare paths (context), aliased specs
//	                      (mypkg "some/pkg"), or blank imports (_ "some/pkg")
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/imports"
)

const marker = "//cicd:astest"

// funcInfo holds the name and extra (non-testing-T) parameters of a marked function.
type funcInfo struct {
	name        string
	extraParams []paramField
}

// paramField holds the names and rendered type string of one parameter field.
type paramField struct {
	names []string
	typ   string
}

// builtinIdents is the set of predeclared Go type identifiers that must not be
// qualified with a package name when rendering a type expression.
var builtinIdents = map[string]bool{
	"bool": true, "byte": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "any": true, "comparable": true,
}

// renderType converts an AST type expression to its string representation,
// qualifying bare identifiers (types local to the source package) with srcPkg.
// Types that are already selector expressions (pkg.Type) are emitted as-is.
// go/printer is used as a fallback for uncommon composite forms.
func renderType(expr ast.Expr, srcPkg string) string {
	switch e := expr.(type) {
	case *ast.Ident:
		if builtinIdents[e.Name] {
			return e.Name
		}
		return srcPkg + "." + e.Name
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			return id.Name + "." + e.Sel.Name
		}
	case *ast.StarExpr:
		return "*" + renderType(e.X, srcPkg)
	case *ast.ArrayType:
		if e.Len == nil {
			return "[]" + renderType(e.Elt, srcPkg)
		}
		if lit, ok := e.Len.(*ast.BasicLit); ok {
			return "[" + lit.Value + "]" + renderType(e.Elt, srcPkg)
		}
	case *ast.MapType:
		return "map[" + renderType(e.Key, srcPkg) + "]" + renderType(e.Value, srcPkg)
	case *ast.ChanType:
		prefix := "chan "
		switch e.Dir {
		case ast.SEND:
			prefix = "chan<- "
		case ast.RECV:
			prefix = "<-chan "
		}
		return prefix + renderType(e.Value, srcPkg)
	case *ast.Ellipsis:
		return "..." + renderType(e.Elt, srcPkg)
	}
	var buf bytes.Buffer
	printer.Fprint(&buf, token.NewFileSet(), expr) //nolint:errcheck
	return buf.String()
}

// stringSliceFlag is a repeatable string flag (flag.Var).
type stringSliceFlag []string

func (f *stringSliceFlag) String() string     { return strings.Join(*f, ", ") }
func (f *stringSliceFlag) Set(v string) error { *f = append(*f, stripQuotes(v)); return nil }

// stripQuotes removes a single matching pair of outer ' or " quotes, if present.
func stripQuotes(s string) string {
	if len(s) >= 2 && ((s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"')) {
		return s[1 : len(s)-1]
	}
	return s
}

func main() {
	pkgPathFlag := flag.Bool("pkg-path", false, "treat first argument as an import path rather than a directory")
	internalTestFlag := flag.Bool("internal_test", false, "use an internal test package (package <pkg>) instead of an external one (package <pkg>_test) when the output file is in the same directory as the source")
	matchFlag := flag.String("match", "", "regular expression; only marked functions whose names match are generated (default: all marked functions)")
	preambleFlag := flag.String("preamble", "", "Go code inserted as the first statement(s) in every generated function; use \\n for multiple lines")
	var extraImports stringSliceFlag
	flag.Var(&extraImports, "import", "extra import spec added to generated file; may be repeated.\n\tbare path: context  aliased: mypkg \"some/pkg\"  blank: _ \"some/pkg\"")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: astest [flags] <package-dir-or-import-path> <output-file>\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	*matchFlag = stripQuotes(*matchFlag)
	*preambleFlag = stripQuotes(*preambleFlag)

	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(1)
	}

	var pkgDir, importPath string
	if *pkgPathFlag {
		var err error
		pkgDir, importPath, err = resolvePackageDir(flag.Arg(0))
		if err != nil {
			log.Fatalf("resolving import path %s: %v", flag.Arg(0), err)
		}
	} else {
		var err error
		pkgDir, err = filepath.Abs(flag.Arg(0))
		if err != nil {
			log.Fatalf("resolving package directory: %v", err)
		}
		importPath, err = findImportPath(pkgDir)
		if err != nil {
			log.Fatalf("finding import path: %v", err)
		}
	}

	outFile, err := filepath.Abs(flag.Arg(1))
	if err != nil {
		log.Fatalf("resolving output file: %v", err)
	}

	var matchRE *regexp.Regexp
	if *matchFlag != "" {
		matchRE, err = regexp.Compile(*matchFlag)
		if err != nil {
			log.Fatalf("compiling --match regexp: %v", err)
		}
	}

	funcs, srcPkgName, err := findMarkedFunctions(pkgDir, matchRE)
	if err != nil {
		log.Fatalf("parsing package: %v", err)
	}

	if len(funcs) == 0 {
		fmt.Fprintf(os.Stderr, "no %s functions found in %s\n", marker, pkgDir)
		return
	}

	outDir := filepath.Dir(outFile)
	var outPkgName string
	if outDir == pkgDir {
		outPkgName = srcPkgName
	} else {
		outPkgName, err = inferPackageName(outDir)
		if err != nil {
			log.Fatalf("inferring output package name: %v", err)
		}
	}
	if !*internalTestFlag {
		outPkgName += "_test"
	}

	code, err := generateCode(outPkgName, srcPkgName, importPath, strings.ReplaceAll(*preambleFlag, `\n`, "\n"), outFile, []string(extraImports), funcs)
	if err != nil {
		log.Fatalf("generating code: %v", err)
	}

	if err := os.WriteFile(outFile, code, 0o644); err != nil {
		log.Fatalf("writing output: %v", err)
	}
	fmt.Printf("wrote %s (%d function(s))\n", outFile, len(funcs))
}

// findMarkedFunctions parses the non-test Go files in dir and returns info
// about all functions that:
//   - start with "Test"
//   - have at least one parameter (first field must have exactly one name and
//     not be *testing.T)
//   - carry the //cicd:astest marker in the doc-comment or as the first
//     comment inside the function body
//   - match filter (if non-nil)
//
// Extra parameters beyond the first are collected for forwarding in the
// generated wrapper. It also returns the package name.
func findMarkedFunctions(dir string, filter *regexp.Regexp) (funcs []funcInfo, pkgName string, err error) {
	fset := token.NewFileSet()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}

	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			return nil, "", err
		}
		if pkgName == "" {
			pkgName = f.Name.Name
		} else if f.Name.Name != pkgName {
			return nil, "", fmt.Errorf("multiple packages in %s; expected exactly one", dir)
		}
		files = append(files, f)
	}

	if len(files) == 0 {
		return nil, "", fmt.Errorf("no Go source files found in %s", dir)
	}

	seen := make(map[string]bool)
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if !isEligibleTestFunc(fn) {
				continue
			}
			if !hasMarker(fn, file) {
				continue
			}
			name := fn.Name.Name
			if seen[name] {
				continue
			}
			if filter != nil && !filter.MatchString(name) {
				continue
			}
			seen[name] = true
			info := funcInfo{name: name}
			for _, field := range fn.Type.Params.List[1:] {
				var names []string
				for _, n := range field.Names {
					names = append(names, n.Name)
				}
				info.extraParams = append(info.extraParams, paramField{
					names: names,
					typ:   renderType(field.Type, pkgName),
				})
			}
			funcs = append(funcs, info)
		}
	}

	sort.Slice(funcs, func(i, j int) bool { return funcs[i].name < funcs[j].name })
	return funcs, pkgName, nil
}

// isEligibleTestFunc reports whether fn looks like a test-helper function:
//   - name begins with "Test"
//   - is a package-level function, not a method
//   - at least one parameter, with exactly one name in the first field
//   - first parameter type is not *testing.T (i.e. it uses a custom interface)
//
// Additional parameters beyond the first are allowed and will be forwarded in
// the generated wrapper via zero-value var declarations.
func isEligibleTestFunc(fn *ast.FuncDecl) bool {
	if !strings.HasPrefix(fn.Name.Name, "Test") {
		return false
	}
	if fn.Recv != nil {
		return false
	}
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 || len(fn.Type.Params.List[0].Names) != 1 {
		return false
	}
	// Exclude functions already using *testing.T — wrapping them would
	// produce a duplicate definition.
	return !isStarTestingT(fn.Type.Params.List[0].Type)
}

// isStarTestingT reports whether expr is the type *testing.T.
func isStarTestingT(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return id.Name == "testing" && sel.Sel.Name == "T"
}

// hasMarker reports whether fn carries the //cicd:astest marker. The marker
// is recognised in two positions:
//  1. In the doc-comment immediately preceding the func keyword.
//  2. In the first comment inside the function body (including an inline
//     comment on the opening-brace line, e.g. `func Foo(t T) { //cicd:astest`).
func hasMarker(fn *ast.FuncDecl, file *ast.File) bool {
	// 1. Doc comment.
	if fn.Doc != nil {
		for _, c := range fn.Doc.List {
			if strings.TrimSpace(c.Text) == marker {
				return true
			}
		}
	}

	// 2. First comment inside the function body.
	if fn.Body == nil {
		return false
	}

	bodyOpen := fn.Body.Lbrace

	// Upper bound: the start of the first statement, or the closing brace if
	// the body is empty.
	var firstStmtPos token.Pos
	if len(fn.Body.List) > 0 {
		firstStmtPos = fn.Body.List[0].Pos()
	} else {
		firstStmtPos = fn.Body.Rbrace
	}

	for _, cg := range file.Comments {
		cgPos := cg.Pos()
		if cgPos <= bodyOpen {
			continue // before or at '{', skip
		}
		if cgPos >= firstStmtPos {
			break // past the first statement (or closing brace)
		}
		for _, c := range cg.List {
			if strings.TrimSpace(c.Text) == marker {
				return true
			}
		}
	}

	return false
}

// resolvePackageDir runs "go list -json <importPath>" and returns the on-disk
// directory and canonical import path for the named package.
func resolvePackageDir(importPath string) (dir, canonical string, err error) {
	out, err := exec.Command("go", "list", "-json", importPath).Output()
	if err != nil {
		return "", "", fmt.Errorf("go list -json %s: %w", importPath, err)
	}
	var info struct {
		Dir        string
		ImportPath string
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return "", "", fmt.Errorf("parsing go list output: %w", err)
	}
	return info.Dir, info.ImportPath, nil
}

// inferPackageName returns the package name for dir by reading an existing
// non-test .go file. Falls back to the directory basename if none are found.
func inferPackageName(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			continue
		}
		return f.Name.Name, nil
	}
	return filepath.Base(dir), nil
}

// importSpec normalises an import value supplied via --import into a valid
// import spec. A bare path like "context" becomes `"context"`; a value that
// already contains a double-quote (e.g. `mypkg "some/pkg"`) is used as-is.
func importSpec(s string) string {
	if strings.ContainsRune(s, '"') {
		return s
	}
	return fmt.Sprintf("%q", s)
}

// generateCode produces a formatted Go source file containing *testing.T
// wrappers for the named functions exported by the given package.
// outPkg is the package declaration for the output file; srcPkg is the
// identifier used to call the source functions. preamble, if non-empty, is
// emitted as the first statement(s) inside every generated function body.
// extraImports are additional import specs written into the import block.
// outFile is the destination path passed to goimports for module-aware import
// resolution.
func generateCode(outPkg, srcPkg, importPath, preamble, outFile string, extraImports []string, funcs []funcInfo) ([]byte, error) {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "// Code generated by astest. DO NOT EDIT.\n\n")
	fmt.Fprintf(&buf, "package %s\n\n", outPkg)
	fmt.Fprintf(&buf, "import (\n")
	fmt.Fprintf(&buf, "\t\"testing\"\n\n")
	fmt.Fprintf(&buf, "\t%q\n", importPath)
	for _, imp := range extraImports {
		fmt.Fprintf(&buf, "\t%s\n", importSpec(imp))
	}
	fmt.Fprintf(&buf, ")\n\n")

	for _, fn := range funcs {
		fmt.Fprintf(&buf, "func %s(t *testing.T) {\n", fn.name)
		var callArgs strings.Builder
		callArgs.WriteString("t")
		for _, p := range fn.extraParams {
			nameList := strings.Join(p.names, ", ")
			fmt.Fprintf(&buf, "\tvar %s %s\n", nameList, p.typ)
			callArgs.WriteString(", ")
			callArgs.WriteString(nameList)
		}
		if preamble != "" {
			for line := range strings.SplitSeq(preamble, "\n") {
				fmt.Fprintf(&buf, "\t%s\n", line)
			}
		}
		fmt.Fprintf(&buf, "\t%s.%s(%s)\n", srcPkg, fn.name, callArgs.String())
		fmt.Fprintf(&buf, "}\n\n")
	}

	return imports.Process(outFile, buf.Bytes(), nil)
}

// findImportPath walks up from dir to locate the nearest go.mod and derives
// the import path as <module>/<relative-path>.
func findImportPath(dir string) (string, error) {
	d := dir
	for {
		modPath := filepath.Join(d, "go.mod")
		if data, err := os.ReadFile(modPath); err == nil {
			f, err := modfile.ParseLax(modPath, data, nil)
			if err != nil {
				return "", fmt.Errorf("parsing %s: %w", modPath, err)
			}
			if f.Module == nil {
				return "", fmt.Errorf("no module directive in %s", modPath)
			}
			modulePath := f.Module.Mod.Path
			rel, err := filepath.Rel(d, dir)
			if err != nil {
				return "", err
			}
			if rel == "." {
				return modulePath, nil
			}
			return modulePath + "/" + filepath.ToSlash(rel), nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return "", fmt.Errorf("no go.mod found in %s or any parent directory", dir)
}
