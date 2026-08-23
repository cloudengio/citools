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
	"slices"

	"cloudeng.io/cmdutil/cmdyaml"
	"cloudeng.io/macos/buildtools"
	"cloudeng.io/macos/macosutils"
	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
)

// serviceLabel is the launchd label / LaunchAgent identifier for the orchestrator
// login service.
const serviceLabel = internal.BundleID

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

	LaunchAgentFile string `subcmd:"launch-agent-file,,'path to the launchd service configuration; defaults to the copy in the app bundle, or to built-in defaults'"`
}

//go:embed launch_agent.yml
var defaultLaunchAgentYAML []byte

// loadLaunchAgentConfig returns the configuration for the login service, and a
// description of where it came from. An explicit path is used as given;
// otherwise the copy shipped in the app bundle is preferred, falling back to the
// defaults built into this binary when running outside a bundle.
func loadLaunchAgentConfig(ctx context.Context, path string) (LaunchAgentConfig, string, error) {
	source := path
	if source == "" {
		if bundled, ok := bundledLaunchAgentConfig(); ok {
			source = bundled
		}
	}
	var lc LaunchAgentConfig
	if source == "" {
		if err := cmdyaml.ParseConfigsStrict(&lc, defaultLaunchAgentYAML); err != nil {
			return lc, "", fmt.Errorf("parsing the built-in service configuration: %w", err)
		}
		return lc, "built-in defaults", nil
	}
	if err := cmdyaml.ParseConfigFilesStrict(ctx, &lc, source); err != nil {
		return lc, "", fmt.Errorf("reading the service configuration %q: %w", source, err)
	}
	return lc, source, nil
}

// bundledLaunchAgentConfig returns the path of the service configuration inside
// the app bundle this executable belongs to.
//
// InAppBundle searches the bundle whose Contents/MacOS holds the running
// executable, which covers running the orchestrator as a bundle's main
// executable. It is not enough on its own here: the orchestrator ships in a
// bundle nested inside the launcher app, and the resource lives in the outer
// one, so each enclosing bundle is searched in turn as well.
func bundledLaunchAgentConfig() (string, bool) {
	exe, err := macosutils.ExecutablePath()
	if err != nil {
		return "", false
	}
	subBundle, ok := macosutils.InBundle(exe, "Contents", "MacOS")
	if !ok {
		return "", false
	}
	parentBundle, ok := macosutils.InBundle(subBundle, "Contents", "MacOS")
	if !ok {
		return "", false
	}
	return macosutils.LocateInBundle(parentBundle, internal.LaunchAgentFileName, macosutils.IsReadable)
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

	lc, source, err := loadLaunchAgentConfig(ctx, fv.LaunchAgentFile)
	if err != nil {
		return err
	}
	agent := launchAgent(lc, exe, config)
	// The plist names log files in this directory; launchd will not start a job
	// whose Standard*Path directory does not exist.
	if err := os.MkdirAll(lc.LogDirOrDefault(), 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}
	if err := runSteps(ctx, agent.Install()...); err != nil {
		return err
	}
	path, _ := agent.PlistPath()
	fmt.Printf("installed and loaded login service %s\n  agent:      %s\n  executable: %s\n  config:     %s\n  service:    %s\n",
		serviceLabel, path, exe, config, source)
	return nil
}

func (ServiceCommand) Uninstall(ctx context.Context, _ any, _ []string) error {
	if err := runSteps(ctx, serviceAgent().Uninstall()...); err != nil {
		return err
	}
	fmt.Printf("removed login service %s\n", serviceLabel)
	return nil
}

func (ServiceCommand) Status(ctx context.Context, _ any, _ []string) error {
	agent := serviceAgent()
	if !agent.IsInstalled() {
		fmt.Println("login service is not installed")
		return nil
	}
	// Status succeeds whether or not launchd has the job loaded; its output,
	// which is the point of the command, goes to stdout via the CommandRunner.
	return runSteps(ctx, agent.Status())
}

func (ServiceCommand) Restart(ctx context.Context, _ any, _ []string) error {
	if err := runSteps(ctx, serviceAgent().Restart()); err != nil {
		return err
	}
	fmt.Printf("restarted login service %s\n", serviceLabel)
	return nil
}

// launchAgent describes the orchestrator's login service, as configured by the
// launch_agent section of configFile, running exe against that same file.
func launchAgent(lc LaunchAgentConfig, exe, configFile string) buildtools.LaunchAgent {
	args := append([]string{exe, "--config", configFile}, lc.RunArgsOrDefault()...)
	logDir := lc.LogDirOrDefault()
	return buildtools.LaunchAgent{
		Plist: buildtools.LaunchAgentPlist{
			Label:                serviceLabel,
			ProgramArguments:     args,
			RunAtLoad:            lc.RunAtLoadOrDefault(),
			KeepAlive:            lc.KeepAliveOrDefault(),
			EnvironmentVariables: lc.EnvironmentOrDefault(),
			StandardOutPath:      filepath.Join(logDir, serviceLabel+".out.log"),
			StandardErrorPath:    filepath.Join(logDir, serviceLabel+".err.log"),
		},
	}
}

// serviceAgent identifies an already-installed login service. Uninstall, status
// and restart act on the installed job, which the Label alone locates, so they
// need none of the configuration that describes how to run it.
func serviceAgent() buildtools.LaunchAgent {
	return buildtools.LaunchAgent{
		Plist: buildtools.LaunchAgentPlist{Label: serviceLabel},
	}
}

// runSteps executes steps, passing their output through to this process so
// that launchctl's diagnostics reach the user.
func runSteps(ctx context.Context, steps ...buildtools.Step) error {
	cmdRunner := buildtools.NewCommandRunner(
		buildtools.WithStdout(os.Stdout),
		buildtools.WithStderr(os.Stderr))
	return buildtools.NewRunner().AddSteps(steps...).Run(ctx, cmdRunner).Error()
}
