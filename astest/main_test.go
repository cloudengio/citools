// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// parseSrc parses a Go source snippet and returns the AST file.
func parseSrc(t *testing.T, src string) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return f
}

// funcDecl finds a named *ast.FuncDecl in a parsed file, failing the test if
// it is not present.
func funcDecl(t *testing.T, f *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("function %q not found in parsed file", name)
	return nil
}

func TestHasMarker_InlineComment(t *testing.T) {
	src := `package p
type T interface{}
func TestFoo(t T) { //cicd:astest
	_ = 1
}
`
	f := parseSrc(t, src)
	fn := funcDecl(t, f, "TestFoo")
	if !hasMarker(fn, f) {
		t.Error("expected hasMarker to return true for inline //cicd:astest")
	}
}

func TestHasMarker_DocComment(t *testing.T) {
	src := `package p
type T interface{}
//cicd:astest
func TestBar(t T) {
	_ = 1
}
`
	f := parseSrc(t, src)
	fn := funcDecl(t, f, "TestBar")
	if !hasMarker(fn, f) {
		t.Error("expected hasMarker to return true for doc-comment //cicd:astest")
	}
}

func TestHasMarker_Absent(t *testing.T) {
	src := `package p
type T interface{}
// Some other comment.
func TestBaz(t T) {
	// Another comment, but not the marker.
	_ = 1
}
`
	f := parseSrc(t, src)
	fn := funcDecl(t, f, "TestBaz")
	if hasMarker(fn, f) {
		t.Error("expected hasMarker to return false when marker is absent")
	}
}

func TestHasMarker_MarkerNotFirst(t *testing.T) {
	// Marker appears after the first statement — must NOT be detected.
	src := `package p
type T interface{}
func TestQux(t T) {
	_ = 1
	//cicd:astest
}
`
	f := parseSrc(t, src)
	fn := funcDecl(t, f, "TestQux")
	if hasMarker(fn, f) {
		t.Error("expected hasMarker to return false when marker is after the first statement")
	}
}

func TestIsEligibleTestFunc(t *testing.T) {
	cases := []struct {
		src      string
		funcName string
		want     bool
	}{
		{
			src: `package p
type T interface{}
func TestOK(t T) {}`,
			funcName: "TestOK",
			want:     true,
		},
		{
			src: `package p
import "testing"
func TestSkip(t *testing.T) {}`,
			funcName: "TestSkip",
			want:     false, // *testing.T param must be excluded
		},
		{
			src: `package p
type T interface{}
func NotATest(t T) {}`,
			funcName: "NotATest",
			want:     false, // doesn't start with "Test"
		},
		{
			src: `package p
func TestNoParams() {}`,
			funcName: "TestNoParams",
			want:     false, // no parameters
		},
		{
			src: `package p
type T interface{}
func TestMultiParams(t T, x int) {}`,
			funcName: "TestMultiParams",
			want:     false, // more than one parameter
		},
		{
			src: `package p
type T interface{}
type Suite struct{}
func (s *Suite) TestMethod(t T) {}`,
			funcName: "TestMethod",
			want:     false, // methods are not eligible; generated wrapper would not compile
		},
	}

	for _, tc := range cases {
		f := parseSrc(t, tc.src)
		fn := funcDecl(t, f, tc.funcName)
		got := isEligibleTestFunc(fn)
		if got != tc.want {
			t.Errorf("isEligibleTestFunc(%s) = %v, want %v", tc.funcName, got, tc.want)
		}
	}
}

func TestFindMarkedFunctions(t *testing.T) {
	dir := t.TempDir()

	const src = `package mypkg

type TestingT interface {
	Helper()
	Fatalf(string, ...any)
}

//cicd:astest
func TestWithDocMarker(t TestingT) {
	t.Helper()
}

func TestWithInlineMarker(t TestingT) { //cicd:astest
	t.Helper()
}

func TestNoMarker(t TestingT) {
	t.Helper()
}
`
	if err := os.WriteFile(filepath.Join(dir, "pkg.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	funcs, pkgName, err := findMarkedFunctions(dir)
	if err != nil {
		t.Fatalf("findMarkedFunctions: %v", err)
	}
	if pkgName != "mypkg" {
		t.Errorf("pkgName = %q, want %q", pkgName, "mypkg")
	}
	want := []string{"TestWithDocMarker", "TestWithInlineMarker"}
	if len(funcs) != len(want) {
		t.Fatalf("found %v, want %v", funcs, want)
	}
	for i, w := range want {
		if funcs[i] != w {
			t.Errorf("funcs[%d] = %q, want %q", i, funcs[i], w)
		}
	}
}

func TestFindMarkedFunctions_SkipsTestFiles(t *testing.T) {
	dir := t.TempDir()

	// Function in a _test.go file should be ignored.
	const testSrc = `package mypkg_test

import "testing"

//cicd:astest
func TestInTestFile(t *testing.T) {}
`
	if err := os.WriteFile(filepath.Join(dir, "pkg_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	const mainSrc = `package mypkg

type T interface{}

//cicd:astest
func TestInMainFile(t T) {}
`
	if err := os.WriteFile(filepath.Join(dir, "pkg.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	funcs, _, err := findMarkedFunctions(dir)
	if err != nil {
		t.Fatalf("findMarkedFunctions: %v", err)
	}
	if len(funcs) != 1 || funcs[0] != "TestInMainFile" {
		t.Errorf("got %v, want [TestInMainFile]", funcs)
	}
}

func TestGenerateCode(t *testing.T) {
	code, err := generateCode("mypkg_test", "mypkg", "example.com/mypkg", "", filepath.Join(t.TempDir(), "out_test.go"), nil, []string{"TestFoo", "TestBar"})
	if err != nil {
		t.Fatalf("generateCode: %v", err)
	}
	s := string(code)
	for _, want := range []string{
		"package mypkg_test",
		`"testing"`,
		`"example.com/mypkg"`,
		"func TestFoo(t *testing.T) {",
		"mypkg.TestFoo(t)",
		"func TestBar(t *testing.T) {",
		"mypkg.TestBar(t)",
		"Code generated by astest",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("generated code missing %q\nfull output:\n%s", want, s)
		}
	}
}

func TestFindImportPath(t *testing.T) {
	root := t.TempDir()
	const goMod = "module example.com/mymod\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "pkg", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findImportPath(sub)
	if err != nil {
		t.Fatalf("findImportPath: %v", err)
	}
	if want := "example.com/mymod/pkg/sub"; got != want {
		t.Errorf("findImportPath(sub) = %q, want %q", got, want)
	}

	got, err = findImportPath(root)
	if err != nil {
		t.Fatalf("findImportPath (root): %v", err)
	}
	if want := "example.com/mymod"; got != want {
		t.Errorf("findImportPath(root) = %q, want %q", got, want)
	}
}
