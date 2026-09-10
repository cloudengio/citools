// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"os/exec"

	"cloudeng.io/cmdutil/cmdyaml"
	"github.com/cloudengio/citools/runners/macos/orchestrator/internal/ui"
)

// LaunchCommand runs the orchestrator in standalone GUI application mode
// (with a Dock icon and status item). It is the entry point when the user
// launches the macOS .app bundle directly from Finder or the Dock.
type LaunchCommand struct{}

const dialogTitle = "GitHub Runner Orchestrator"

func (LaunchCommand) Run(ctx context.Context, _ any, _ []string) error {
	u := ui.New(ui.ModeRegular)

	cfgPath, _, err := installMinimalConfigIfMissing()
	if err != nil {
		u.Notify(dialogTitle, "Failed to create the configuration file:\n\n"+err.Error())
		return err
	}

	// If the login service is already installed, open the Web UI or inform the user.
	if serviceAgent().IsInstalled() {
		var cfg Config
		err := cmdyaml.ParseConfigFilesStrict(ctx, &cfg, cfgPath)
		webURL := ""
		if err == nil && cfg.WebUI.Enabled && cfg.WebUI.ListenAddress != "" {
			webURL = formatWebURL(cfg.WebUI.ListenAddress)
		}
		if webURL != "" {
			_ = exec.Command("open", webURL).Start()
		} else {
			u.Notify(dialogTitle, "The GitHub Runner Orchestrator is already installed and running as a login service.")
		}
		return nil
	}

	// On first launch / uninstalled, offer to install the login service automatically.
	if u.Confirm(dialogTitle, "Start the GitHub Runner Orchestrator automatically when you log in?") {
		if err := installLoginService(ctx, "", cfgPath, "", false); err != nil {
			u.Notify(dialogTitle, "Failed to install the login service:\n\n"+err.Error())
			return err
		}
		u.Notify(dialogTitle, "Installed. The orchestrator will start automatically when you log in.")
		return nil
	}

	if globalFlags.ConfigFile == "github_orchestrator_config.yml" {
		globalFlags.ConfigFile = cfgPath
	}

	ctx, _, postConfig, err := configPrehook(ctx)
	if err != nil {
		u.Notify(dialogTitle, err.Error())
		return err
	}
	defer func(c context.Context) { _, _ = postConfig(c) }(ctx)

	ctx, _, postKeys, err := withKeysPrehook(ctx)
	if err != nil {
		u.Notify(dialogTitle, err.Error())
		return err
	}
	defer func(c context.Context) { _, _ = postKeys(c) }(ctx)

	ctx, _, postRepos, err := repoClientsPrehook(ctx)
	if err != nil {
		u.Notify(dialogTitle, err.Error())
		return err
	}
	defer func(c context.Context) { _, _ = postRepos(c) }(ctx)

	// Run the orchestrator in-process in regular (Dock-visible) mode.
	runCmd := RunCommand{}
	runFlags := &RunFlags{
		DeleteOrphanedVMs: true,
	}
	return runCmd.runWithUIMode(ctx, runFlags, ui.ModeRegular)
}
