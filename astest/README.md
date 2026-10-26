# [cloudeng.io/citools/astest](https://pkg.go.dev/cloudeng.io/citools/astest?tab=doc)


usage: `astest` [flags] <package-dir-or-import-path> <output-file>

    -import value
      	extra import spec added to generated file; may be repeated.
      		bare path: context  aliased: mypkg "some/pkg"  blank: _ "some/pkg"
    -internal-test
      	use an internal test package (package <pkg>) instead of an external one (package <pkg>_test) when the output file is in the same directory as the source
    -match string
      	regular expression; only marked functions whose names match are generated (default: all marked functions)
    -pkg-path
      	treat first argument as an import path rather than a directory
    -postamble string
      	Go code inserted after the fixture call in every generated function; use \n for multiple lines
    -preamble string
      	Go code inserted as the first statement(s) in every generated function; use \n for multiple lines
    -test-functions-list string
      	name of a public var of type []func(<testing-t-type>) initialised with every generated wrapper; empty means no list is generated
    -testing-t-type string
      	type of the first parameter in every generated wrapper function (default: *testing.T);
      		use e.g. testing.TB to accept any testing interface (default "*testing.T")
    -variadic string
      	variable name to use in the wrapper for a fixture variadic parameter named _;
      		if set, the variable is declared and suppressed (_ = <name>) so preamble/postamble can reference it;
      		if unset (default), blank variadic parameters are omitted from the generated wrapper
    -wrapper string
      	expression used to wrap the first argument in each generated call;
      		e.g. --wrapper mypkg.NewT produces pkg.TestFoo(mypkg.NewT(t), ...)

Command `astest` generates wrapper functions for functions that accept
a TestingT interface and carry the //cicd:`astest` marker. This lets
test-helper packages expose reusable test logic (callable with any TestingT
implementation) while also making that logic directly runnable by `go
test`. By default the generated wrapper parameter type is *testing.T;
use --testing-t-type to select a different type such as testing.TB.

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

    --pkg-path             treat the first argument as an import path rather than
                           a directory; the directory is resolved via go list
    --internal-test        use an internal test package (package <pkg>);
                           default is the external form (package <pkg>_test)
    --match <regexp>       only generate wrappers for marked functions whose
                           names match the regular expression (default: all)
    --preamble <code>      Go statements inserted at the top of every generated
                           function body, before the fixture call;
                           use \n to separate multiple statements
    --postamble <code>     Go statements inserted at the bottom of every generated
                           function body, after the fixture call;
                           use \n to separate multiple statements
    --wrapper <expr>       expression used to wrap the first argument (t) in each
                           generated call; e.g. --wrapper mypkg.NewT produces
                           pkg.TestFoo(mypkg.NewT(t), ...)
    --variadic <name>      variable name to use in the wrapper for a fixture variadic
                           parameter named _; if set, the variable is declared and
                           suppressed (_ = <name>) so preamble/postamble can reference it;
                           if unset (default), blank variadic parameters are omitted
    --testing-t-type <type>  type of the first parameter (t) in every generated wrapper
                             function (default: *testing.T); use e.g. testing.TB to accept
                             any standard testing interface
    --test-functions-list <name> generate a public var of type []func(<testing-t-type>)
                             initialised with every generated wrapper function;
                             empty (default) means no list is generated
    --import <spec>        extra import added to the generated file; may be
                           repeated. Accepts bare paths (context), aliased specs
                           (mypkg "some/pkg"), or blank imports (_ "some/pkg")


