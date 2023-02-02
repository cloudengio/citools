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
	TestCmd   []string `yaml:"test_cmd,flow"`
	LintCmd   []string `yaml:"lint_cmd,flow"`
	GoVulnCmd []string `yaml:"govuln_cmd,flow"`
}

func readConfig() (config, error) {
	var c config
	data, err := os.ReadFile(configFile)
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
	configFile  string
	testFlag    bool
	modulesFlag bool
	lintFlag    bool
	goVulnFlag  bool
)

func init() {
	flag.BoolVar(&modulesFlag, "modules", false, "print modules in this repo")
	flag.BoolVar(&testFlag, "test", false, "run tests")
	flag.BoolVar(&lintFlag, "lint", false, "run lint")
	flag.BoolVar(&goVulnFlag, "govuln", false, "run govuln")
	flag.StringVar(&configFile, "config", ".multimod.yml", "config file")
}

func main() {
	ctx := context.Background()
	flag.Parse()
	mods, err := modules()
	if err != nil {
		done("finding modules", err)
	}
	if modulesFlag || (!testFlag && !lintFlag && !goVulnFlag) {
		fmt.Println(strings.Join(mods, " "))
		return
	}
	cfg, err := readConfig()
	if err != nil {
		done("reading config", err)
	}

	if testFlag {
		if err := runInDirs(ctx, mods, cfg.TestCmd); err != nil {
			done("test: ", err)
		}
	}

	if lintFlag {
		if err := runInDirs(ctx, mods, cfg.LintCmd); err != nil {
			done("lint: ", err)
		}
	}

	if goVulnFlag {
		if err := runInDirs(ctx, mods, cfg.GoVulnCmd); err != nil {
			done("govuln: ", err)
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
