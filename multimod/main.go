// Copyright 2023 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

// multimod provides a means of running commands across multiple modules in a
// a single repository. It operates by scanning the filesystem for all
// directories that contain a go.mod file (including 'sub-modules').
// It can then perform a set of 'actions' in each of these directories
// in turn. The available actions are specified in a yaml config file and
// consist of a list of commands to be executed in each directory.
// For example, the 'lint' action will run the following command in
// each module directory.
//
//	lint: ["golangci-lint", "run", "./..."]
//
// Note that that ';' can be used as a command separator to specify multiple
// commands to be run, as in:
//
//	update: ["go", "get", "-u", "./...", ";", "go", "mod", "tidy"]
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"cloudeng.io/errors"
	"golang.org/x/mod/modfile"
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
	TestCmd    []string            `yaml:"test,flow"`
	LintCmd    []string            `yaml:"lint,flow"`
	GoVulnCmd  []string            `yaml:"govuln,flow"`
	Generate   []string            `yaml:"generate,flow"`
	Markdown   []string            `yaml:"markdown,flow"`
	Usage      []string            `yaml:"usage,flow"`
	Annotate   []string            `yaml:"annotate,flow"`
	Update     []string            `yaml:"update,flow"`
	Exclusions map[string][]string `yaml:"exclusions"`
}

func (c config) commandForAction(action string) []string {
	t := reflect.TypeOf(c)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if strings.HasPrefix(field.Tag.Get("yaml"), action) {
			val := reflect.ValueOf(c)
			cmd := val.Field(i).Interface().([]string)
			return cmd
		}
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
	fmt.Printf("multimod: %s: %v\n", msg, err)
	os.Exit(1)
}

type script struct {
	action   string
	commands []string
}

var (
	configFileFlag        string
	modulesFlag           bool
	verboseFlag           bool
	goworkUpdateFlag      bool
	localGoWorkUpdateFlag bool
)

func init() {
	flag.BoolVar(&modulesFlag, "modules", false, "print modules in this repo")
	flag.StringVar(&configFileFlag, "config", "", "config file")
	flag.BoolVar(&verboseFlag, "verbose", false, "verbose output")
	flag.BoolVar(&goworkUpdateFlag, "gowork-update", false, "update all go.work references to latest git hash")
	flag.BoolVar(&localGoWorkUpdateFlag, "gowork-update-local", false, "update go.work references for the specified local modules (comman separated) only")
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
	if goworkUpdateFlag {
		if err := goworkUpdate(ctx, nil); err != nil {
			done("updating go.work references", err)
		}
		return
	}
	if localGoWorkUpdateFlag {
		if err := goworkUpdate(ctx, flag.Args()); err != nil {
			done("updating go.work references", err)
		}
		return
	}

	actions := flag.Args()
	if modulesFlag && len(actions) == 0 {
		fmt.Println(strings.Join(mods, " "))
		return
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
		exclusions := cfg.Exclusions[script.action]
		allowedMods := []string{}
		for _, mod := range mods {
			if !slices.Contains(exclusions, mod) {
				allowedMods = append(allowedMods, mod)
			} else {
				fmt.Printf("Excluding module %q from action %q\n", mod, script.action)
			}
		}
		if err := runInDirs(ctx, allowedMods, script.action, script.commands); err != nil {
			done(fmt.Sprintf("running %v", script.action), err)
		}
	}
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
				return nil
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
		var errs errors.M
		for _, dir := range dirs {
			if err := runInDir(ctx, dir, cmd, args); err != nil {
				fmt.Fprintf(os.Stderr, "%v: failed: %v\n", dir, err)
				errs.Append(fmt.Errorf("action in %v: %v %v %w", dir, action, strings.Join(cmdargs, " "), err))
			}
		}
		if err := errs.Err(); err != nil {
			return err
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

func goworkUpdate(ctx context.Context, internalModsToConsider []string) error {
	filename := "go.work"
	contents, err := os.ReadFile(filename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Printf("No go.work file found at %v\n", filename)
			return nil
		}
		return err
	}
	fmt.Printf("Updating go.work references in %v\n", filename)
	wk, err := modfile.ParseWork(filename, contents, nil)
	if err != nil {
		return err
	}

	type perModUpdate struct {
		mod    string
		update string
	}

	updates := []perModUpdate{}
	modFiles := map[string]*modfile.File{}
	var internalMods, externalMods []string
	for _, r := range wk.Use {
		if r.Path == "." || strings.Contains(r.Path, "multimod") {
			continue
		}

		h, err := gitHashFor(ctx, r.Path)
		if err != nil {
			return fmt.Errorf("failed to get git hash for %v: %v", r.Path, err)
		}
		mod, err := readGoMod(r.Path)
		if err != nil {
			return err
		}
		modFiles[r.Path] = mod
		updates = append(updates, perModUpdate{
			mod:    mod.Module.Mod.Path,
			update: mod.Module.Mod.Path + "@" + h,
		})
		if len(r.Path) > 2 && r.Path[0] == '.' && r.Path[1] == '/' {
			internalMods = append(internalMods, r.Path)
		} else {
			externalMods = append(externalMods, r.Path)
		}
	}

	// for external modules apply all updates to every module
	// in this workspace.
	for _, modpath := range externalMods {
		for _, update := range updates {
			if err := runInDir(ctx, modpath, "go", []string{"get", update.update}); err != nil {
				return fmt.Errorf("%v: go get %v: failed %w", modpath, update.update, err)
			}
			if err := runInDir(ctx, modpath, "go", []string{"mod", "tidy"}); err != nil {
				return fmt.Errorf("%v: go mod tidy: failed %w", modpath, err)
			}
		}
	}

	if len(internalModsToConsider) == 0 {
		return nil
	}
	cleaned := []string{}
	for _, m := range internalModsToConsider {
		cleaned = append(cleaned, filepath.Clean(m))
	}
	// for internal modules only apply updates for other modules,
	// avoid updating a module with itself.
	for _, modpath := range internalMods {
		if !slices.Contains(cleaned, filepath.Clean(modpath)) {
			continue
		}
		otherUpdates := []string{}
		for _, update := range updates {
			mf := modFiles[modpath]
			if mf.Module.Mod.Path == update.mod {
				fmt.Printf("Skipping update of %v in %v to itself\n", update.mod, modpath)
				continue
			}
			otherUpdates = append(otherUpdates, update.update)
		}
		merged := []string{"get"}
		merged = append(merged, otherUpdates...)
		if err := runInDir(ctx, modpath, "go", merged); err != nil {
			return fmt.Errorf("%v: go get %v: failed %w", modpath, merged, err)
		}
		if err := runInDir(ctx, modpath, "go", []string{"mod", "tidy"}); err != nil {
			return fmt.Errorf("%v: go mod tidy: failed %w", modpath, err)
		}
	}
	return nil
}

func readGoMod(path string) (*modfile.File, error) {
	filename := filepath.Join(path, "go.mod")
	contents, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return modfile.Parse(path, contents, nil)
}

func gitHashFor(ctx context.Context, path string) (string, error) {
	var out strings.Builder
	c := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	c.Dir = path
	c.Stderr = os.Stderr
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()[:8]), nil
}
