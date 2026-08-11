// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"cloudeng.io/logging/ctxlog"
)

// isWebappBuildInvocation reports whether the process was invoked to run the
// webapp-build subcommand. "webapp-build" is a distinctive literal that never
// appears as a global flag value, so a plain argument scan is reliable and lets
// main skip config/keychain loading for this tooling-only command.
func isWebappBuildInvocation() bool {
	return slices.Contains(os.Args[1:], "webapp-build")
}

// WebappBuildCommand builds the embedded web UI frontend by running the npm
// pipeline (install, regenerate the typed API client, and build the bundle) that
// produces webui/frontend/dist, which is embedded into the binary by the webui
// package.
type WebappBuildCommand struct{}

type WebappBuildFlags struct {
	Dir         string `subcmd:"dir,webui/frontend,path to the web UI frontend directory"`
	SkipInstall bool   `subcmd:"skip-install,false,skip 'npm install'"`
	SkipGen     bool   `subcmd:"skip-gen,false,skip regenerating the typed API client from openapi.yaml"`
}

func (WebappBuildCommand) Run(ctx context.Context, flags any, _ []string) error {
	fv := flags.(*WebappBuildFlags)

	dir, err := filepath.Abs(fv.Dir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		return fmt.Errorf("no package.json found in %s: %w", dir, err)
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return fmt.Errorf("npm not found on PATH: %w", err)
	}

	var steps [][]string
	if !fv.SkipInstall {
		steps = append(steps, []string{"install"})
	}
	if !fv.SkipGen {
		steps = append(steps, []string{"run", "gen"})
	}
	steps = append(steps, []string{"run", "build"})

	for _, args := range steps {
		if err := runNpm(ctx, dir, args...); err != nil {
			return fmt.Errorf("npm %v failed: %w", args, err)
		}
	}
	ctxlog.Info(ctx, "web ui build complete", "dir", dir)
	fmt.Printf("web UI built into %s\n", filepath.Join(dir, "dist"))
	return nil
}

func runNpm(ctx context.Context, dir string, args ...string) error {
	ctxlog.Info(ctx, "running npm", "args", args, "dir", dir)
	fmt.Printf("==> npm %v (in %s)\n", args, dir)
	cmd := exec.CommandContext(ctx, "npm", args...) //nolint:gosec // G204: args are fixed literals.
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
