// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

// Command orchestrator-launcher is the macOS .app bundle entry point. It is a
// thin wrapper around the github-runner-orchestrator binary that lives beside it
// in the bundle. Because a .app is launched with no controlling terminal, a
// failure of the orchestrator would otherwise vanish into the system log; the
// launcher surfaces first-launch setup and any failure through native dialogs.
//
// It keeps all of this app/GUI-specific behaviour out of the orchestrator
// itself, which remains a plain command-line tool. The launcher drives the
// orchestrator's own subcommands (install, service install, run).
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"

	"cloudeng.io/macos/macosutils"
	"cloudeng.io/os/executil"
	"cloudeng.io/text/textutil"
	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
)

const (
	// logTailBytes bounds how much of the end of the log file is read to find
	// the lines shown in the failure dialog, and maxDialogLines how many of
	// those lines are shown; the full log is always on disk.
	logTailBytes   = 64 * 1024
	maxDialogLines = 20

	orchestratorBinary = internal.OrchestratorBinary
	serviceLabel       = internal.BundleID
	dialogTitle        = "GitHub Runner Orchestrator"
)

func main() {
	// runApp runs the launcher. On macOS it runs it inside a Cocoa event loop so
	// the Dock icon stays put (no bounce) and the app can be quit from the Dock;
	// elsewhere it just runs the launcher directly.
	runApp()
}

// runLauncher performs the launcher's work: locate the orchestrator, seed config,
// optionally install the login service, otherwise run the orchestrator in this
// session surfacing any failure in a dialog.
func runLauncher() {
	// The launcher is entered from a cgo //export, so there is no context to
	// inherit. A plain background context is deliberate: the orchestrator is
	// shut down by TerminateLaunchedApp, which signals it to exit gracefully,
	// whereas cancelling the context would have exec kill it outright.
	ctx := context.Background()

	orch, err := orchestratorPath()
	if err != nil {
		notify("Cannot locate the orchestrator executable:\n" + err.Error())
		return
	}
	cfg, err := configPath()
	if err != nil {
		notify("Cannot determine the configuration path:\n" + err.Error())
		return
	}

	// Seed the minimal config the first time the app is opened.
	created := false
	if _, err := os.Stat(cfg); errors.Is(err, os.ErrNotExist) {
		if out, err := run(ctx, orch, "install"); err != nil {
			notify("Failed to create the configuration file:\n\n" + out)
			return
		}
		created = true
	}

	// On first launch, offer to run the orchestrator automatically at login.
	if created && !macosutils.IsServiceInstalled(serviceLabel) {
		if confirm("Start the GitHub Runner Orchestrator automatically when you log in?") {
			if out, err := run(ctx, orch, "service", "install"); err != nil {
				notify("Failed to install the login service:\n\n" + out)
			} else {
				notify("Installed. The orchestrator will start automatically when you log in.")
			}
			return
		}
	}

	// Otherwise run the orchestrator in this session, surfacing any failure.
	runOrchestrator(ctx, orch, cfg)
}

// orchestratorPath returns the path to the orchestrator binary. The launcher is
// the main executable of the outer app, and the orchestrator is the main
// executable of the bundle nested at <app>/Contents/MacOS/<nested>.app.
// LocateInBundle searches the whole outer bundle, so the exact nesting is not
// hard-coded here.
func orchestratorPath() (string, error) {
	bundle, ok := macosutils.ProcessInBundle()
	if !ok {
		return "", fmt.Errorf("the launcher is not running from an app bundle")
	}
	path, ok := macosutils.LocateInBundle(bundle, orchestratorBinary, macosutils.IsExecutable)
	if !ok {
		return "", fmt.Errorf("%v not found in the app bundle %v", orchestratorBinary, bundle)
	}
	return path, nil
}

// configPath returns the per-user config location, matching the orchestrator's
// own default (~/Library/Application Support/github-runner-orchestrator/...).
func configPath() (string, error) {
	return executil.UserConfigDirPath(
		filepath.Join(internal.ConfigDir, internal.ConfigFileName))
}

// logPath returns the file the orchestrator's output is streamed to.
func logPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", serviceLabel, internal.OrchestratorBinary+".log")
}

// run executes the orchestrator with args and returns its combined output. It is
// used for the short-lived setup subcommands (install, service install), which
// run before runOrchestrator has built its Launcher and whose output is reported
// in a dialog rather than streamed to the log.
func run(ctx context.Context, orch string, args ...string) (string, error) {
	return macosutils.NewLauncher(macosutils.WithCmdEnv(orchestratorEnv)).RunApp(ctx, orch, args...)
}

// orchestratorEnv returns the environment for the orchestrator with common
// Homebrew locations added to PATH, so tools it invokes (tart, go, docker)
// are found even when the app is launched from Finder/LaunchServices, which
// provides only a minimal PATH.
func orchestratorEnv() []string {
	path := executil.AppendMissingPathComponents(os.Getenv("PATH"),
		"/opt/homebrew/bin", "/usr/local/bin")
	return executil.ReplaceEnvVar(os.Environ(), "PATH", path)
}

// orchestratorLauncher holds the Launcher running the orchestrator so that it
// can be terminated when the app is quit from the Dock (which does not deliver a
// Unix signal to this process). It is package level, and populated only once
// runOrchestrator has built it, because the Cocoa delegate calls terminateChild
// from outside the launcher's own call stack.
var orchestratorLauncher atomic.Pointer[macosutils.Launcher]

// terminateChild signals the running orchestrator to shut down. Called from the
// app's applicationWillTerminate when the user quits. It is a no-op if the
// orchestrator is not running: TerminateLaunchedApp reports whether it signalled
// anything, which the Cocoa delegate has no use for, and only marks the run as
// deliberately stopped when there was something to stop.
func terminateChild() {
	if l := orchestratorLauncher.Load(); l != nil {
		l.TerminateLaunchedApp()
	}
}

// runOrchestrator runs `orchestrator --config <cfg> run --delete-orphaned-vms`,
// streaming its combined output to the log file and, if it exits with an error,
// showing the tail of that output in a dialog. Interrupt/terminate signals are
// forwarded so quitting the app shuts the orchestrator down cleanly.
func runOrchestrator(ctx context.Context, orch, cfg string) {
	lp := logPath()
	if err := os.MkdirAll(filepath.Dir(lp), 0o755); err != nil {
		notify("Failed to create the log directory:\n" + err.Error())
		return
	}

	// A Launcher accepts a single LaunchApp, so this one belongs to this run and
	// is published only for the lifetime of that run. The orchestrator writes
	// its own log to lp, so its stdout and stderr are left alone.
	l := macosutils.NewLauncher(
		macosutils.WithCmdEnv(orchestratorEnv))
	orchestratorLauncher.Store(l)
	defer orchestratorLauncher.Store(nil)

	// LaunchApp forwards interrupt/terminate signals to the orchestrator and
	// reports success once TerminateLaunchedApp has been called, so quitting the
	// app does not raise a failure dialog.
	err := l.LaunchApp(ctx, orch, "--log-file", lp, "--config", cfg, "run", "--delete-orphaned-vms")
	switch {
	case err == nil:
	case errors.Is(err, macosutils.ErrFailedToLaunch):
		// Nothing ever ran, so there is no output to show.
		notify("Failed to start the orchestrator:\n" + err.Error())
	default:
		notify(fmt.Sprintf(
			"The GitHub Runner Orchestrator stopped with an error (%v).\n\n%s\n\nFull log: %s",
			err, logTail(lp), lp))
	}
}

// logTail returns the last maxDialogLines of the log file at path, for display
// in the failure dialog. Only the end of the file is read: the orchestrator is
// long running and launchd restarts it, so the log may be large. A file that
// cannot be read yields nothing, leaving the dialog to report just the error.
func logTail(path string) []byte {
	buf, err := macosutils.TailBytes(path, logTailBytes)
	if err != nil {
		return nil
	}
	// Reading from an offset can start mid-line; Tail drops that partial line
	// along with everything before the last maxDialogLines.
	return textutil.Tail(buf, '\n', maxDialogLines)
}

// confirm shows a native two-button dialog and reports whether the affirmative
// ("Install") button was chosen. Any failure (no GUI session, cancelled) is a
// decline.
func confirm(message string) bool {
	script := fmt.Sprintf(
		`display dialog %q buttons {"Not Now","Install"} default button "Install" with title %q`,
		message, dialogTitle)
	out, err := exec.Command("osascript", "-e", script, "-e", "button returned of result").Output() //nolint:gosec // fixed script, values quoted.
	return err == nil && string(bytes.TrimRight(out, "\r\n")) == "Install"
}

// notify shows a native informational dialog. Failures are ignored.
func notify(message string) {
	script := fmt.Sprintf(`display dialog %q buttons {"OK"} default button "OK" with title %q`, message, dialogTitle)
	_ = exec.Command("osascript", "-e", script).Run() //nolint:gosec // fixed script, values quoted.
}
