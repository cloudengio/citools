// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"cloudeng.io/os/executil"

	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
)

// minimalConfigYAML is the minimal, valid orchestrator configuration installed
// as a starting point. It is also embedded into the app bundle by the bundle
// command (Contents/Resources).
//
//go:embed minimal_config.yml
var minimalConfigYAML []byte

// InstallCommand writes the bundled minimal configuration file to a standard
// location. It is the runtime side of the installer app bundle built by
// `orchestrator bundle`: double-clicking the bundle runs it (see
// appBundleInstallLaunch).
type InstallCommand struct{}

type InstallFlags struct {
	Out   string `subcmd:"out,,path to write the config file; defaults to a per-user application-support location"`
	Force bool   `subcmd:"force,false,overwrite the config file if it already exists"`
}

func (InstallCommand) Run(_ context.Context, fl any, _ []string) error {
	fv := fl.(*InstallFlags)

	out := fv.Out
	if out == "" {
		p, err := defaultInstallPath()
		if err != nil {
			return err
		}
		out = p
	}
	if !fv.Force {
		if _, err := os.Stat(out); err == nil {
			return fmt.Errorf("config file %s already exists (use --force to overwrite)", out)
		}
	}
	if err := writeMinimalConfig(out); err != nil {
		return err
	}
	fmt.Printf("wrote minimal configuration to %s\n", out)
	return nil
}

// writeMinimalConfig writes the embedded minimal config to out, creating any
// parent directories. It overwrites out if it exists.
func writeMinimalConfig(out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := os.WriteFile(out, minimalConfigYAML, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// installMinimalConfigIfMissing writes the minimal config to the default
// per-user location if it does not already exist, and returns that path and
// whether it was created. This is the preinstall step run when the installer app
// is launched: the config is created once, then left alone so user edits persist
// across launches.
func installMinimalConfigIfMissing() (path string, created bool, err error) {
	out, err := defaultInstallPath()
	if err != nil {
		return "", false, err
	}
	if _, err := os.Stat(out); os.IsNotExist(err) {
		if err := writeMinimalConfig(out); err != nil {
			return "", false, err
		}
		return out, true, nil
	}
	return out, false, nil
}

// defaultInstallPath returns the per-user location for the installed config
// (~/Library/Application Support/github-runner-orchestrator on macOS).
func defaultInstallPath() (string, error) {
	return executil.UserConfigDirPath(
		filepath.Join(internal.ConfigDir, internal.ConfigFileName))
}
