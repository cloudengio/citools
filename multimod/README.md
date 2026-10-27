# [cloudeng.io/citools/multimod](https://pkg.go.dev/cloudeng.io/citools/multimod?tab=doc)


Usage of
/var/folders/6l/tm8174_x64j4xsrtfw4mbf380000gn/T/go-build1683469252/b001/exe/multimod:

    -concurrent
      	run commands concurrently in all modules
    -config string
      	config file
    -gowork-update
      	update all go.work references to latest git hash
    -gowork-update-local
      	update go.work references for the specified local modules only
    -modules
      	print modules in this repo
    -verbose
      	verbose output

`multimod` provides a means of running commands across multiple modules
in a a single repository. It operates by scanning the filesystem for
all directories that contain a go.mod file (including 'sub-modules').
It can then perform a set of 'actions' in each of these directories in turn.
The available actions are specified in a yaml config file and consist of a
list of commands to be executed in each directory. For example, the 'lint'
action will run the following command in each module directory.

    lint: ["golangci-lint", "run", "./..."]

Note that that ';' can be used as a command separator to specify multiple
commands to be run, as in:

    update: ["go", "get", "-u", "./...", ";", "go", "mod", "tidy"]


