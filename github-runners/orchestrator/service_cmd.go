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
	"strings"

	"howett.net/plist"
)

// serviceLabel is the launchd label / LaunchAgent identifier for the orchestrator
// login service.
const serviceLabel = "io.cloudeng.github-runner-orchestrator"

// ServiceCommand manages the orchestrator as a per-user launchd login service
// (a LaunchAgent), which starts the orchestrator when the user logs in.
//
// A LaunchAgent (not a root LaunchDaemon) is used deliberately: the orchestrator
// spawns tart VMs, and tart's Virtualization.framework can only start VMs from
// within a logged-in user's GUI session. Combine this with automatic login to
// start the orchestrator at boot.
type ServiceCommand struct{}

// isServiceInvocation reports whether the process was invoked to run a service
// subcommand, which manages launchd and needs no orchestrator config or keychain.
func isServiceInvocation() bool {
	return slices.Contains(os.Args[1:], "service")
}

type ServiceInstallFlags struct {
	Executable string `subcmd:"executable,,path to the orchestrator executable; defaults to this binary"`
	Config     string `subcmd:"config-file,,path to the config file; defaults to the per-user location"`
}

func (ServiceCommand) Install(ctx context.Context, fl any, _ []string) error {
	fv := fl.(*ServiceInstallFlags)

	exe := fv.Executable
	if exe == "" {
		e, err := os.Executable()
		if err != nil {
			return fmt.Errorf("determining executable path: %w", err)
		}
		exe = e
	}
	config := fv.Config
	if config == "" {
		c, _, err := installMinimalConfigIfMissing()
		if err != nil {
			return err
		}
		config = c
	}

	if err := installService(ctx, exe, config); err != nil {
		return err
	}
	path, _ := launchAgentPlistPath()
	fmt.Printf("installed and loaded login service %s\n  agent:      %s\n  executable: %s\n  config:     %s\n",
		serviceLabel, path, exe, config)
	return nil
}

func (ServiceCommand) Uninstall(ctx context.Context, _ any, _ []string) error {
	if err := uninstallService(ctx); err != nil {
		return err
	}
	fmt.Printf("removed login service %s\n", serviceLabel)
	return nil
}

func (ServiceCommand) Status(ctx context.Context, _ any, _ []string) error {
	if !isServiceInstalled() {
		fmt.Println("login service is not installed")
		return nil
	}
	cmd := exec.CommandContext(ctx, "launchctl", "print", serviceTarget())
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	_ = cmd.Run() // launchctl prints a useful message even when not loaded.
	return nil
}

func (ServiceCommand) Restart(ctx context.Context, _ any, _ []string) error {
	if err := runLaunchctl(ctx, "kickstart", "-k", serviceTarget()); err != nil {
		return err
	}
	fmt.Printf("restarted login service %s\n", serviceLabel)
	return nil
}

// installService writes the LaunchAgent plist and (re)loads it via launchctl.
func installService(ctx context.Context, exe, config string) error {
	data, err := buildLaunchAgentPlist(exe, config)
	if err != nil {
		return err
	}
	path, err := launchAgentPlistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating LaunchAgents directory: %w", err)
	}
	if err := os.MkdirAll(logDir(), 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing LaunchAgent: %w", err)
	}
	// Reload: bootout any previously-loaded instance (ignore errors if none),
	// then bootstrap the current one.
	_ = runLaunchctl(ctx, "bootout", serviceTarget())
	if err := runLaunchctl(ctx, "bootstrap", guiDomain(), path); err != nil {
		return fmt.Errorf("loading LaunchAgent: %w", err)
	}
	return nil
}

// uninstallService unloads and removes the LaunchAgent.
func uninstallService(ctx context.Context) error {
	_ = runLaunchctl(ctx, "bootout", serviceTarget())
	path, err := launchAgentPlistPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing LaunchAgent: %w", err)
	}
	return nil
}

func isServiceInstalled() bool {
	path, err := launchAgentPlistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func guiDomain() string     { return fmt.Sprintf("gui/%d", os.Getuid()) }
func serviceTarget() string { return guiDomain() + "/" + serviceLabel }

func logDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs")
}

func launchAgentPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist"), nil
}

// buildLaunchAgentPlist renders the LaunchAgent plist that runs the orchestrator
// with `run --delete-orphaned-vms` against config.
func buildLaunchAgentPlist(exe, config string) ([]byte, error) {
	dict := map[string]any{
		"Label":            serviceLabel,
		"ProgramArguments": []any{exe, "--config", config, "run", "--delete-orphaned-vms"},
		"RunAtLoad":        true,
		"KeepAlive":        true,
		// launchd provides a minimal PATH; add the Homebrew locations so the
		// orchestrator can find the tools it invokes (tart, go, docker).
		"EnvironmentVariables": map[string]any{
			"PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		},
		"StandardOutPath":   filepath.Join(logDir(), serviceLabel+".out.log"),
		"StandardErrorPath": filepath.Join(logDir(), serviceLabel+".err.log"),
	}
	return plist.MarshalIndent(dict, plist.XMLFormat, "\t")
}

func runLaunchctl(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "launchctl", args...).CombinedOutput() //nolint:gosec // G204: args are internal, not user input.
	if err != nil {
		return fmt.Errorf("launchctl %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return nil
}
