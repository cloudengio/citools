// usage: astest [flags] <package-dir-or-import-path> <output-file>
//
//	-import value
//	  	extra import spec added to generated file; may be repeated.
//	  		bare path: context  aliased: mypkg "some/pkg"  blank: _ "some/pkg"
//	-internal-test
//	  	use an internal test package (package <pkg>) instead of an external one (package <pkg>_test) when the output file is in the same directory as the source
//	-match string
//	  	regular expression; only marked functions whose names match are generated (default: all marked functions)
//	-pkg-path
//	  	treat first argument as an import path rather than a directory
//	-postamble string
//	  	Go code inserted after the fixture call in every generated function; use \n for multiple lines
//	-preamble string
//	  	Go code inserted as the first statement(s) in every generated function; use \n for multiple lines
//	-test-functions-list string
//	  	name of a public var of type []func(<testing-t-type>) initialised with every generated wrapper; empty means no list is generated
//	-testing-t-type string
//	  	type of the first parameter in every generated wrapper function (default: *testing.T);
//	  		use e.g. testing.TB to accept any testing interface (default "*testing.T")
//	-variadic string
//	  	variable name to use in the wrapper for a fixture variadic parameter named _;
//	  		if set, the variable is declared and suppressed (_ = <name>) so preamble/postamble can reference it;
//	  		if unset (default), blank variadic parameters are omitted from the generated wrapper
//	-wrapper string
//	  	expression used to wrap the first argument in each generated call;
//	  		e.g. --wrapper mypkg.NewT produces pkg.TestFoo(mypkg.NewT(t), ...)
package main
