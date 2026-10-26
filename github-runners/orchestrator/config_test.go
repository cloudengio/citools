// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/cmdconfig"
	"cloudeng.io/cmdutil/flags"
	cerrors "cloudeng.io/errors"
	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
)

func TestValidateListenAddress(t *testing.T) {
	valid := []string{
		"127.0.0.1:8088",
		"127.0.0.1",
		"localhost:8088",
		"localhost",
	}
	for _, addr := range valid {
		if err := validateListenAddress(addr); err != nil {
			t.Errorf("validateListenAddress(%q) returned error: %v, want nil", addr, err)
		}
	}

	invalid := []string{
		":8088",
		"0.0.0.0:8088",
		"0.0.0.0",
		"192.168.1.1:8088",
		"8.8.8.8:8088",
		"",
	}
	for _, addr := range invalid {
		if err := validateListenAddress(addr); err == nil {
			t.Errorf("validateListenAddress(%q) succeeded, want error", addr)
		}
	}
}

func TestLogDirOrDefault(t *testing.T) {
	home, _ := os.UserHomeDir()
	tests := []struct {
		input string
		want  string
	}{
		{"", filepath.Join(home, "Library", "Logs")},
		{"~", home},
		{"~/test_logs", filepath.Join(home, "test_logs")},
		{"/var/log", "/var/log"},
	}

	for _, tc := range tests {
		cfg := LaunchAgentConfig{LogDir: tc.input}
		got := cfg.LogDirOrDefault()
		if !strings.HasPrefix(got, "/") {
			t.Errorf("LogDirOrDefault(%q) = %q; want absolute path", tc.input, got)
		}
		if home != "" && got != tc.want {
			t.Errorf("LogDirOrDefault(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestIsCleanShutdown covers this command's delegation to
// cmdutil.IsCleanShutdown rather than the predicate itself, which cmdutil
// tests: that the default sentinels apply and, above all, that every error is
// required to be clean, so that a run which was interrupted and also failed is
// still reported as a failure.
func TestIsCleanShutdown(t *testing.T) {
	if !isCleanShutdown(nil) {
		t.Errorf("isCleanShutdown(nil) = false, want true")
	}

	if !isCleanShutdown(context.Canceled) {
		t.Errorf("isCleanShutdown(context.Canceled) = false, want true")
	}

	if !isCleanShutdown(cmdutil.ErrInterrupt) {
		t.Errorf("isCleanShutdown(cmdutil.ErrInterrupt) = false, want true")
	}

	realErr := errors.New("something crashed")
	if isCleanShutdown(realErr) {
		t.Errorf("isCleanShutdown(realErr) = true, want false")
	}

	var mixed cerrors.M
	mixed.Append(realErr)
	mixed.Append(context.Canceled)
	if isCleanShutdown(mixed.Err()) {
		t.Errorf("isCleanShutdown(a failure alongside a cancellation) = true, want false")
	}
}

// newConfigFlag returns the flag as it is defined in GlobalFlags, ie. with
// the default files.
func newConfigFlag(t *testing.T, set ...string) *cmdconfig.FilesFlag {
	t.Helper()
	var globals GlobalFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	if err := flags.RegisterFlagsInStruct(fs, "subcmd", &globals, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(set) > 0 {
		if err := fs.Parse([]string{"--config", strings.Join(set, ",")}); err != nil {
			t.Fatal(err)
		}
	}
	return &globals.Config
}

func TestConfigFlagDefaults(t *testing.T) {
	ff := newConfigFlag(t)
	if got, want := ff.Files(cmdconfig.FileTypeYAML, false), []string{internal.ConfigFileName}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got, want := ff.Files(cmdconfig.FileTypeEnv, false), []string{DefaultEnvConfigFile}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if ff.HasSet(cmdconfig.FileTypeYAML) || ff.HasSet(cmdconfig.FileTypeEnv) {
		t.Error("the defaults should not be reported as set")
	}
}

func TestLocateConfigFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Nothing is found, which is not an error for the defaults.
	loc, err := newConfigFlag(t).Locate(ConfigSpec())
	if err != nil || len(loc.YAML) != 0 || len(loc.Env) != 0 {
		t.Fatalf("got %+v, %v", loc, err)
	}

	// GITHUB_ORCHESTRATOR_CONFIG overrides the default.
	tmpDir := t.TempDir()
	customCfg := filepath.Join(tmpDir, "custom_orchestrator.yml")
	if err := os.WriteFile(customCfg, []byte("global:\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ConfigOrchestratorEnvVar, customCfg)
	if loc, err = newConfigFlag(t).Locate(ConfigSpec()); err != nil || !slices.Equal(loc.YAML, []string{customCfg}) {
		t.Errorf("got %+v, %v, want %v", loc, err, customCfg)
	}

	// ... as does GITHUB_ORCHESTRATOR_ENV_CONFIG, and what it names must exist.
	envPath := filepath.Join(tmpDir, "test.env")
	if err := os.WriteFile(envPath, []byte("FOO=bar\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvOrchestratorEnvVar, envPath)
	if loc, err = newConfigFlag(t).Locate(ConfigSpec()); err != nil || !slices.Equal(loc.Env, []string{envPath}) {
		t.Errorf("got %+v, %v, want %v", loc, err, envPath)
	}
	t.Setenv(EnvOrchestratorEnvVar, filepath.Join(tmpDir, "absent.env"))
	if _, err = newConfigFlag(t).Locate(ConfigSpec()); err == nil {
		t.Error("expected an error for an environment variables file that is not there")
	}

	// The command line takes precedence over the environment variables, and
	// the files it names must exist.
	t.Setenv(EnvOrchestratorEnvVar, "")
	explicit := filepath.Join(tmpDir, "explicit.yml")
	if err := os.WriteFile(explicit, []byte("global:\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if loc, err = newConfigFlag(t, explicit, envPath).Locate(ConfigSpec()); err != nil ||
		!slices.Equal(loc.YAML, []string{explicit}) || !slices.Equal(loc.Env, []string{envPath}) {
		t.Errorf("got %+v, %v", loc, err)
	}
	if _, err = newConfigFlag(t, "/explicit/absent.yml").Locate(ConfigSpec()); err == nil {
		t.Error("expected an error for a configuration file that is not there")
	}
}

func TestLoadConfigWithEnv(t *testing.T) {
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, "test.env")
	envContent := "RELAY_HOST=https://relay.example.com\nWEB_ADDR=127.0.0.1:9099\n"
	if err := os.WriteFile(envFile, []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}

	cfgFile := filepath.Join(tmpDir, "config.yml")
	cfgContent := `
webhook:
  relay_url: ${RELAY_HOST}/events
web_ui:
  listen_address: ${WEB_ADDR}
`
	if err := os.WriteFile(cfgFile, []byte(cfgContent), 0600); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if _, err := cmdconfig.Load(context.Background(), &cfg, newConfigFlag(t, cfgFile, envFile), ConfigSpec()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got, want := cfg.Webhook.RelayURL, "https://relay.example.com/events"; got != want {
		t.Errorf("Webhook.RelayURL = %q, want %q", got, want)
	}
	if got, want := cfg.WebUI.ListenAddress, "127.0.0.1:9099"; got != want {
		t.Errorf("WebUI.ListenAddress = %q, want %q", got, want)
	}
}
