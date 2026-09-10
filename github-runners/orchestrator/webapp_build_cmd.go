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

	"cloudeng.io/logging/ctxlog"
)

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

func buildWebapp(ctx context.Context, dir string, skipInstall, skipGen bool) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(absDir, "package.json")); err != nil {
		return fmt.Errorf("no package.json found in %s: %w", absDir, err)
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return fmt.Errorf("npm not found on PATH: %w", err)
	}

	var steps [][]string
	if !skipInstall {
		steps = append(steps, []string{"install"})
	}
	if !skipGen {
		steps = append(steps, []string{"run", "gen"})
	}
	steps = append(steps, []string{"run", "build"})

	for _, args := range steps {
		if err := runNpm(ctx, absDir, args...); err != nil {
			return fmt.Errorf("npm %v failed: %w", args, err)
		}
	}
	ctxlog.Info(ctx, "web ui build complete", "dir", absDir)
	fmt.Printf("web UI built into %s\n", filepath.Join(absDir, "dist"))
	return nil
}

func (WebappBuildCommand) Run(ctx context.Context, flags any, _ []string) error {
	fv := flags.(*WebappBuildFlags)
	return buildWebapp(ctx, fv.Dir, fv.SkipInstall, fv.SkipGen)
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
