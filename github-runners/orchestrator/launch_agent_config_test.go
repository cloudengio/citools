// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloudeng.io/cmdutil/cmdyaml"
)

//go:fix inline
func boolPtr(v bool) *bool { return new(v) }

// TestLaunchAgentConfigDefaults verifies that an unset section reproduces the
// service the orchestrator has always installed: the accessors, not the YAML,
// are what supply the defaults.
func TestLaunchAgentConfigDefaults(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	var lc LaunchAgentConfig

	if !lc.RunAtLoadOrDefault() {
		t.Error("run_at_load defaults to false, want true")
	}
	if !lc.KeepAliveOrDefault() {
		t.Error("keep_alive defaults to false, want true")
	}
	if got, want := lc.LogDirOrDefault(), filepath.Join(home, "Library", "Logs"); got != want {
		t.Errorf("log_dir: got %q, want %q", got, want)
	}
	if got, want := strings.Join(lc.RunArgsOrDefault(), " "), "run --delete-orphaned-vms"; got != want {
		t.Errorf("run_args: got %q, want %q", got, want)
	}
	env := lc.EnvironmentOrDefault()
	if got, want := env["PATH"], DefaultServicePath; got != want {
		t.Errorf("PATH: got %q, want %q", got, want)
	}
}

// TestLaunchAgentConfigOverrides verifies that every field can be overridden,
// including setting the booleans to false, which a plain bool could not express.
func TestLaunchAgentConfigOverrides(t *testing.T) {
	lc := LaunchAgentConfig{
		RunAtLoad:            new(false),
		KeepAlive:            new(false),
		EnvironmentVariables: map[string]string{"PATH": "/custom/bin", "FOO": "bar"},
		LogDir:               "/var/log/orch",
		RunArgs:              []string{"run"},
	}
	if lc.RunAtLoadOrDefault() {
		t.Error("run_at_load: false was not honoured")
	}
	if lc.KeepAliveOrDefault() {
		t.Error("keep_alive: false was not honoured")
	}
	if got, want := lc.LogDirOrDefault(), "/var/log/orch"; got != want {
		t.Errorf("log_dir: got %q, want %q", got, want)
	}
	if got, want := strings.Join(lc.RunArgsOrDefault(), " "), "run"; got != want {
		t.Errorf("run_args: got %q, want %q", got, want)
	}
	if got, want := lc.EnvironmentOrDefault()["FOO"], "bar"; got != want {
		t.Errorf("FOO: got %q, want %q", got, want)
	}

	// An explicit true is distinguishable from unset.
	set := LaunchAgentConfig{RunAtLoad: new(true), KeepAlive: new(true)}
	if !set.RunAtLoadOrDefault() || !set.KeepAliveOrDefault() {
		t.Error("explicitly true booleans were not honoured")
	}
}

// TestLogDirOrDefaultTilde covers the expansion neither the config parser nor
// launchd performs, which a hand-edited configuration will rely on.
func TestLogDirOrDefaultTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	for _, tc := range []struct{ in, want string }{
		{"~/Library/Logs", filepath.Join(home, "Library", "Logs")},
		{"~", home},
		{"/var/log/orch", "/var/log/orch"},
		// Only a leading ~ followed by a separator is a home reference.
		{"~user/logs", "~user/logs"},
		{"/tmp/~/logs", "/tmp/~/logs"},
	} {
		if got := (LaunchAgentConfig{LogDir: tc.in}).LogDirOrDefault(); got != tc.want {
			t.Errorf("LogDirOrDefault(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestLaunchAgentConfigYAML verifies that the standalone file parses into the
// section, including a false that must not be mistaken for absent.
func TestLaunchAgentConfigYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launch_agent.yml")
	if err := os.WriteFile(path, []byte(`
run_at_load: false
keep_alive: true
log_dir: /var/log/orch
run_args: [run]
environment_variables:
  PATH: /custom/bin
`), 0o600); err != nil {
		t.Fatal(err)
	}
	var lc LaunchAgentConfig
	if err := cmdyaml.ParseConfigFilesStrict(context.Background(), &lc, path); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if lc.RunAtLoadOrDefault() {
		t.Error("run_at_load: false was not honoured")
	}
	if !lc.KeepAliveOrDefault() {
		t.Error("keep_alive: true was not honoured")
	}
	if got, want := lc.LogDirOrDefault(), "/var/log/orch"; got != want {
		t.Errorf("log_dir: got %q, want %q", got, want)
	}
}

// TestShippedLaunchAgentConfig verifies that the file shipped in the bundle
// parses and yields the documented defaults, so that editing it cannot silently
// diverge from what the accessors promise.
func TestShippedLaunchAgentConfig(t *testing.T) {
	var lc LaunchAgentConfig
	if err := cmdyaml.ParseConfigsStrict(&lc, defaultLaunchAgentYAML); err != nil {
		t.Fatalf("the embedded launch_agent.yml does not parse: %v", err)
	}
	if !lc.RunAtLoadOrDefault() || !lc.KeepAliveOrDefault() {
		t.Error("the shipped file disagrees with the documented boolean defaults")
	}
	if got, want := strings.Join(lc.RunArgsOrDefault(), " "), "run --delete-orphaned-vms"; got != want {
		t.Errorf("run_args: got %q, want %q", got, want)
	}
	if got, want := lc.EnvironmentOrDefault()["PATH"], DefaultServicePath; got != want {
		t.Errorf("PATH: got %q, want %q", got, want)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	// The shipped file writes ~/Library/Logs, which must expand.
	if got, want := lc.LogDirOrDefault(), filepath.Join(home, "Library", "Logs"); got != want {
		t.Errorf("log_dir: got %q, want %q", got, want)
	}
}

// TestLoadLaunchAgentConfig covers the resolution order: an explicit path, then
// the bundled copy, then the defaults built into the binary.
func TestLoadLaunchAgentConfig(t *testing.T) {
	ctx := context.Background()

	// No path and no enclosing bundle: the embedded defaults are used.
	lc, source, err := loadLaunchAgentConfig(ctx, "")
	if err != nil {
		t.Fatalf("loadLaunchAgentConfig: %v", err)
	}
	if source != "built-in defaults" {
		t.Errorf("source: got %q, want the built-in defaults", source)
	}
	if !lc.KeepAliveOrDefault() {
		t.Error("the built-in defaults disagree with the accessors")
	}

	// An explicit path wins and is reported as the source.
	path := filepath.Join(t.TempDir(), "agent.yml")
	if err := os.WriteFile(path, []byte("keep_alive: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lc, source, err = loadLaunchAgentConfig(ctx, path)
	if err != nil {
		t.Fatalf("loadLaunchAgentConfig: %v", err)
	}
	if source != path {
		t.Errorf("source: got %q, want %q", source, path)
	}
	if lc.KeepAliveOrDefault() {
		t.Error("the explicit file was not honoured")
	}

	// A missing or malformed file is reported rather than falling back.
	if _, _, err := loadLaunchAgentConfig(ctx, filepath.Join(t.TempDir(), "absent.yml")); err == nil {
		t.Error("a missing file was accepted")
	}
	bad := filepath.Join(t.TempDir(), "bad.yml")
	if err := os.WriteFile(bad, []byte("not_a_field: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadLaunchAgentConfig(ctx, bad); err == nil {
		t.Error("an unknown field was accepted by the strict parser")
	}
}
