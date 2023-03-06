// Copyright 2023 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

// multimod provides a means of running commands across multiple modules in a
// a single repository.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const builtinConfig = `
test:
  [
    "go",
    "test",
    "-failfast",
    "--covermode=atomic",
    "--vet=off",
    "-race",
    "./...",
  ]
lint: ["golangci-lint", "run", "./..."]
govuln: ["govulncheck", "./..."]
generate: ["go", "generate", "./..."]
markdown: ["gomarkdown", "-overwrite", "./..."]
annotate:
  [
    "goannotate",
    "--config=${MULTIMOD_ROOT}/copyright-annotation.yaml",
    "--annotation=cloudeng-copyright",
  ]
usage: ["gousage", "--overwrite", "./..."]
update: ["go", "get", "-u", "./...", ";",
         "go", "mod", "tidy"]
`

type config struct {
	TestCmd   []string `yaml:"test,flow"`
	LintCmd   []string `yaml:"lint,flow"`
	GoVulnCmd []string `yaml:"govuln,flow"`
	Generate  []string `yaml:"generate,flow"`
	Markdown  []string `yaml:"markdown,flow"`
	Usage     []string `yaml:"usage,flow"`
	Annotate  []string `yaml:"annotate,flow"`
	Update    []string `yaml:"update,flow"`
}

func (c config) commandForAction(action string) []string {
	switch action {
	case "test":
		return c.TestCmd
	case "lint":
		return c.LintCmd
	case "govuln":
		return c.GoVulnCmd
	case "generate":
		return c.Generate
	case "markdown":
		return c.Markdown
	case "usage":
		return c.Usage
	case "annotate":
		return c.Annotate
	case "update":
		return c.Update
	}
	return nil
}

var multimod_root string

func init() {
	multimod_root, _ = os.Getwd()
}

func expand(command []string) []string {
	mapper := func(varname string) string {
		switch varname {
		case "MULTIMOD_ROOT":
			return multimod_root
		default:
			return ""
		}
	}
	expanded := []string{}
	for _, c := range command {
		expanded = append(expanded, os.Expand(c, mapper))
	}
	return expanded
}

func readConfig() (config, error) {
	var c config
	var data = []byte(builtinConfig)
	var err error
	if len(configFileFlag) > 0 {
		data, err = os.ReadFile(configFileFlag)
		if err != nil {
			return c, err
		}
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return c, err
	}
	return c, nil
}

func done(msg string, err error) {
	fmt.Printf("Failed: %s: %s\n", msg, err)
	os.Exit(1)
}

var (
	configFileFlag string
	modulesFlag    bool
	verboseFlag    bool
)

func init() {
	flag.BoolVar(&modulesFlag, "modules", false, "print modules in this repo")
	flag.StringVar(&configFileFlag, "config", "", "run tests")
	flag.BoolVar(&verboseFlag, "verbose", false, "verbose output")
}

func main() {
	ctx := context.Background()
	flag.Parse()

	cfg, err := readConfig()
	if err != nil {
		done("reading config", err)
	}

	mods, err := modules()
	if err != nil {
		done("finding modules", err)
	}
	actions := flag.Args()
	if modulesFlag && len(actions) == 0 {
		fmt.Println(strings.Join(mods, " "))
		return
	}
	type script struct {
		action   string
		commands []string
	}
	var scripts []script
	for _, action := range actions {
		command := cfg.commandForAction(action)
		if len(command) == 0 {
			done("unsupported action", fmt.Errorf("%q", action))
		}
		command = expand(command)
		scripts = append(scripts, script{action, command})
	}
	for _, script := range scripts {
		if err := runInDirs(ctx, mods, script.action, script.commands); err != nil {
			done("lint: ", err)
		}
	}
	return
}

func modules() ([]string, error) {
	var dirs []string
	err := filepath.Walk(".", func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if _, err := os.Open(filepath.Join(path, "go.mod")); err == nil {
				dirs = append(dirs, path)
				return filepath.SkipDir
			}
		}
		return nil
	})
	return dirs, err
}

func splitCmd(cmdargs []string) [][]string {
	var cmds [][]string
	var cmd []string
	for _, arg := range cmdargs {
		if arg == ";" {
			cmds = append(cmds, cmd)
			cmd = []string{}
		} else {
			cmd = append(cmd, arg)
		}
	}
	if len(cmd) > 0 {
		cmds = append(cmds, cmd)
	}
	return cmds
}

func runInDirs(ctx context.Context, dirs []string, action string, cmdSpec []string) error {
	if len(cmdSpec) == 0 {
		return fmt.Errorf("missing command")
	}

	allCmds := splitCmd(cmdSpec)
	for _, cmdargs := range allCmds {
		cmd := cmdargs[0]
		args := []string{}
		if len(cmdargs) > 1 {
			args = cmdargs[1:]
		}
		failed := false
		for _, dir := range dirs {
			if err := runInDir(ctx, dir, cmd, args); err != nil {
				fmt.Fprintf(os.Stderr, "%v: failed: %v\n", dir, err)
				failed = true
			}
		}
		if failed {
			return fmt.Errorf("%v failed", action)
		}
	}
	return nil
}

func runInDir(ctx context.Context, dir string, binary string, args []string) error {
	fmt.Printf("%v...\n", dir)
	if verboseFlag {
		fmt.Printf("%v %v\n", binary, strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err == nil {
		fmt.Printf("%v... ok\n", dir)
	} else {
		fmt.Printf("%v... failed\n", dir)
	}
	return err
}
