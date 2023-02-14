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

func readConfig() (config, error) {
	var c config
	data, err := os.ReadFile(configFileFlag)
	if err != nil {
		return c, err
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
)

func init() {
	flag.BoolVar(&modulesFlag, "modules", false, "print modules in this repo")
	flag.StringVar(&configFileFlag, "config", "", "run tests")
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
	script := [][]string{}
	for _, action := range actions {
		command := cfg.commandForAction(action)
		if len(command) == 0 {
			done("unsupported action", fmt.Errorf("%q", action))
		}
		script = append(script, command)
	}
	for _, command := range script {
		if err := runInDirs(ctx, mods, command); err != nil {
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

func runInDirs(ctx context.Context, dirs []string, cmdargs []string) error {
	if len(cmdargs) == 0 {
		return fmt.Errorf("missing command")
	}
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
		return fmt.Errorf("tests failed")
	}
	return nil
}

func runInDir(ctx context.Context, dir string, binary string, args []string) error {
	fmt.Printf("%v...\n", dir)
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
