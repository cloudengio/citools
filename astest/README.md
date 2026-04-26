# [cloudeng.io/citools/astest](https://pkg.go.dev/cloudeng.io/citools/astest?tab=doc)


Command `astest` generates *testing.T wrapper functions for functions that
accept a TestingT interface and carry the //cicd:`astest` marker. This lets
test-helper packages expose reusable test logic (callable with any TestingT
implementation) while also making that logic directly runnable by `go test`.

For every function of the form

    func TestFoo(t SomeInterfaceType) { //cicd:astest
        …
    }

(with the marker in the preceding doc-comment), `astest` emits:

    func TestFoo(t *testing.T) { pkg.TestFoo(t) }

The output file may live in the same directory as the source package
(generating an external _test package) or in a different directory (the
package name is inferred from existing files there, falling back to the
directory basename). The source package is always imported.

The output is processed by goimports, so packages referenced by --preamble
or --import are added to (or removed from) the import block automatically.

# Usage

    `astest` [flags] <package-dir-or-import-path> <output-file>

# Flags

    --pkg-path            treat the first argument as an import path rather than
                          a directory; the directory is resolved via go list
    --preamble <code>     Go statements inserted at the top of every generated
                          function body; use \n to separate multiple statements
    --import <spec>       extra import added to the generated file; may be
                          repeated. Accepts bare paths (context), aliased specs
                          (mypkg "some/pkg"), or blank imports (_ "some/pkg")

