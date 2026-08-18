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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

const (
	orchestratorBinary    = "github-runner-orchestrator"
	nestedOrchestratorApp = "github-runner-orchestrator.app"
	serviceLabel          = "io.cloudeng.github-runner-orchestrator"
	dialogTitle           = "GitHub Runner Orchestrator"
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
		if out, err := run(orch, "install"); err != nil {
			notify("Failed to create the configuration file:\n\n" + out)
			return
		}
		created = true
	}

	// On first launch, offer to run the orchestrator automatically at login.
	if created && !serviceInstalled() {
		if confirm("Start the GitHub Runner Orchestrator automatically when you log in?") {
			if out, err := run(orch, "service", "install"); err != nil {
				notify("Failed to install the login service:\n\n" + out)
			} else {
				notify("Installed. The orchestrator will start automatically when you log in.")
			}
			return
		}
	}

	// Otherwise run the orchestrator in this session, surfacing any failure.
	runOrchestrator(orch, cfg)
}

// orchestratorPath returns the path to the orchestrator binary in the nested
// bundle. The launcher lives at <app>/Contents/MacOS/<launcher>; the orchestrator
// is the main executable of the nested bundle at
// <app>/Contents/Library/github-runner-orchestrator.app.
func orchestratorPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	macos := filepath.Dir(exe) // <app>/Contents/MacOS
	return filepath.Join(macos, "..", "Library", nestedOrchestratorApp,
		"Contents", "MacOS", orchestratorBinary), nil
}

// configPath returns the per-user config location, matching the orchestrator's
// own default (~/Library/Application Support/github-runner-orchestrator/...).
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "github-runner-orchestrator", "github_orchestrator_config.yml"), nil
}

// serviceInstalled reports whether the login service (LaunchAgent) is installed.
func serviceInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist"))
	return err == nil
}

// logPath returns the file the orchestrator's output is streamed to.
func logPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", serviceLabel+".out.log")
}

// run executes the orchestrator with args and returns its combined output. It is
// used for the short-lived setup subcommands (install, service install).
func run(orch string, args ...string) (string, error) {
	cmd := exec.Command(orch, args...) //nolint:gosec // args are internal, not user input.
	cmd.Env = orchestratorEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// orchestratorEnv returns the environment for the orchestrator with common
// Homebrew locations prepended to PATH, so tools it invokes (tart, go, docker)
// are found even when the app is launched from Finder/LaunchServices, which
// provides only a minimal PATH.
func orchestratorEnv() []string {
	env := os.Environ()
	path := os.Getenv("PATH")
	var prepend []string
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if !strings.Contains(":"+path+":", ":"+d+":") {
			prepend = append(prepend, d)
		}
	}
	if len(prepend) == 0 {
		return env
	}
	newPath := "PATH=" + strings.Join(prepend, ":") + ":" + path
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = newPath
			return env
		}
	}
	return append(env, newPath)
}

// The orchestrator child process, tracked so it can be terminated when the app
// is quit from the Dock (which does not deliver a Unix signal to this process).
var (
	childMu  sync.Mutex
	child    *exec.Cmd
	quitting atomic.Bool
)

func setChild(c *exec.Cmd) {
	childMu.Lock()
	child = c
	childMu.Unlock()
}

// terminateChild signals the running orchestrator to shut down. Called from the
// app's applicationWillTerminate when the user quits.
func terminateChild() {
	quitting.Store(true)
	childMu.Lock()
	c := child
	childMu.Unlock()
	if c != nil && c.Process != nil {
		_ = c.Process.Signal(syscall.SIGTERM)
	}
}

// runOrchestrator runs `orchestrator --config <cfg> run --delete-orphaned-vms`,
// streaming its combined output to the log file and, if it exits with an error,
// showing the tail of that output in a dialog. Interrupt/terminate signals are
// forwarded so quitting the app shuts the orchestrator down cleanly.
func runOrchestrator(orch, cfg string) {
	lp := logPath()
	_ = os.MkdirAll(filepath.Dir(lp), 0o755)
	logFile, _ := os.OpenFile(lp, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644) //nolint:errcheck
	defer func() {
		if logFile != nil {
			_ = logFile.Close()
		}
	}()

	tail := &tailBuffer{max: 1500}
	var w io.Writer = tail
	if logFile != nil {
		w = io.MultiWriter(logFile, tail)
	}

	cmd := exec.Command(orch, "--config", cfg, "run", "--delete-orphaned-vms") //nolint:gosec // args are internal.
	cmd.Stdout, cmd.Stderr = w, w
	cmd.Env = orchestratorEnv()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	if err := cmd.Start(); err != nil {
		notify("Failed to start the orchestrator:\n" + err.Error())
		return
	}
	setChild(cmd)
	defer setChild(nil)
	go func() {
		for s := range sigCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(s)
			}
		}
	}()

	// Don't show the failure dialog if the process was stopped because the user
	// is quitting the app.
	if err := cmd.Wait(); err != nil && !quitting.Load() {
		notify(fmt.Sprintf(
			"The GitHub Runner Orchestrator stopped with an error (%v).\n\n%s\n\nFull log: %s",
			err, tail.String(), lp))
	}
}

// tailBuffer is a concurrency-safe io.Writer that retains only the last max
// bytes written.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// confirm shows a native two-button dialog and reports whether the affirmative
// ("Install") button was chosen. Any failure (no GUI session, cancelled) is a
// decline.
func confirm(message string) bool {
	script := fmt.Sprintf(
		`display dialog %q buttons {"Not Now","Install"} default button "Install" with title %q`,
		message, dialogTitle)
	out, err := exec.Command("osascript", "-e", script, "-e", "button returned of result").Output() //nolint:gosec // fixed script, values quoted.
	return err == nil && string(trimNL(out)) == "Install"
}

// notify shows a native informational dialog. Failures are ignored.
func notify(message string) {
	script := fmt.Sprintf(`display dialog %q buttons {"OK"} default button "OK" with title %q`, message, dialogTitle)
	_ = exec.Command("osascript", "-e", script).Run() //nolint:gosec // fixed script, values quoted.
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
