// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
)

// writeLines writes n numbered lines to path and returns it.
func writeLines(t *testing.T, path string, n int) string {
	t.Helper()
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "line %04d: %s\n", i, strings.Repeat("x", 60))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLogTail covers what the failure dialog shows. The log is written by a
// long-running service that launchd restarts, so it may be far larger than the
// window read from its end.
func TestLogTail(t *testing.T) {
	dir := t.TempDir()

	// Fewer lines than the cap: all of them, unchanged.
	short := writeLines(t, filepath.Join(dir, "short.log"), 3)
	if got, want := strings.Count(string(logTail(short)), "\n"), 3; got != want {
		t.Errorf("short log: got %d lines, want %d", got, want)
	}

	// More lines than the cap: the last maxDialogLines, and whole ones. The
	// file is larger than logTailBytes, so the read starts mid-line.
	long := writeLines(t, filepath.Join(dir, "long.log"), 5000)
	info, err := os.Stat(long)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= logTailBytes {
		t.Fatalf("test log is %d bytes, want more than the %d byte window", info.Size(), logTailBytes)
	}
	got := string(logTail(long))
	if n := strings.Count(got, "\n"); n != maxDialogLines {
		t.Errorf("long log: got %d lines, want %d", n, maxDialogLines)
	}
	if !strings.HasPrefix(got, fmt.Sprintf("line %04d:", 5000-maxDialogLines)) {
		t.Errorf("long log does not start at a line boundary: %.40q", got)
	}
	if !strings.Contains(got, "line 4999:") {
		t.Errorf("long log does not include the final line")
	}

	// A file that cannot be read yields nothing, so the dialog reports just the
	// error rather than failing.
	if got := logTail(filepath.Join(dir, "no-such.log")); got != nil {
		t.Errorf("missing file: got %q, want nil", got)
	}
	empty := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := logTail(empty); len(got) != 0 {
		t.Errorf("empty file: got %q, want nothing", got)
	}
	// A directory is not readable as a log either.
	if got := logTail(dir); len(got) != 0 {
		t.Errorf("directory: got %q, want nothing", got)
	}
}

// TestOrchestratorEnv covers the PATH repair the launcher makes, since
// Finder and LaunchServices start the app with a minimal one.
func TestOrchestratorEnv(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")

	env := orchestratorEnv()
	var paths []string
	for _, e := range env {
		if after, ok := strings.CutPrefix(e, "PATH="); ok {
			paths = append(paths, after)
		}
	}
	if len(paths) != 1 {
		t.Fatalf("PATH entries: got %d, want exactly 1: %v", len(paths), paths)
	}
	for _, want := range []string{"/usr/bin", "/bin", "/opt/homebrew/bin", "/usr/local/bin"} {
		if !strings.Contains(paths[0], want) {
			t.Errorf("PATH %q does not contain %q", paths[0], want)
		}
	}

	// Directories already present are not duplicated.
	t.Setenv("PATH", "/opt/homebrew/bin:/usr/bin")
	env = orchestratorEnv()
	for _, e := range env {
		if after, ok := strings.CutPrefix(e, "PATH="); ok {
			if n := strings.Count(after, "/opt/homebrew/bin"); n != 1 {
				t.Errorf("PATH %q contains /opt/homebrew/bin %d times, want 1", after, n)
			}
		}
	}
}

// TestPaths covers the per-user locations the launcher derives, which must
// agree with the orchestrator's own defaults.
func TestPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}
	if want := filepath.Join(internal.ConfigDir, internal.ConfigFileName); !strings.HasSuffix(cfg, want) {
		t.Errorf("configPath = %q, want it to end with %q", cfg, want)
	}
	if !filepath.IsAbs(cfg) {
		t.Errorf("configPath = %q, want an absolute path", cfg)
	}

	lp := logPath()
	if want := filepath.Join(home, "Library", "Logs", serviceLabel, internal.OrchestratorBinary+".log"); lp != want {
		t.Errorf("logPath = %q, want %q", lp, want)
	}
}

// TestTerminateChildNotRunning verifies that quitting the app before the
// orchestrator has been launched is a no-op rather than a nil dereference: the
// Cocoa delegate calls this whenever the user quits.
func TestTerminateChildNotRunning(t *testing.T) {
	orchestratorLauncher.Store(nil)
	terminateChild()
}
