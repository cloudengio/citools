// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/cmdconfig"
	"cloudeng.io/cmdutil/cmdyaml"
	"cloudeng.io/cmdutil/subcmd"
)

func TestConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     Config
		wantErr string
		wantP   string
	}{
		{
			name:  "valid prompt",
			cfg:   Config{Prompt: "my custom prompt"},
			wantP: "my custom prompt",
		},
		{
			name:    "empty config",
			cfg:     Config{},
			wantErr: "prompt must not be empty",
		},
		{
			name:    "whitespace only prompt",
			cfg:     Config{Prompt: "   \n\t  "},
			wantErr: "prompt must not be empty",
		},
		{
			name: "valid prompts map only",
			cfg: Config{
				Prompts: map[string]string{
					"claude": "claude prompt",
					"agy":    "agy prompt",
				},
			},
		},
		{
			name: "empty prompt for tool in map",
			cfg: Config{
				Prompts: map[string]string{
					"claude": "   ",
				},
			},
			wantErr: `prompt for "claude" must not be empty`,
		},
		{
			name: "valid prompt and prompts map",
			cfg: Config{
				Prompt: "default prompt",
				Prompts: map[string]string{
					"claude": "claude prompt",
				},
			},
			wantP: "default prompt",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			err := cfg.Validate()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got error %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Prompt != tc.wantP {
				t.Errorf("got prompt %q, want %q", cfg.Prompt, tc.wantP)
			}
		})
	}
}

func TestParseConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	content := `prompt: |
  Custom instructions for PR.
  Line 2.
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	var cfg Config
	if err := cmdyaml.ParseConfigFilesStrict(t.Context(), &cfg, cfgPath); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate config: %v", err)
	}
	if !strings.Contains(cfg.Prompt, "Custom instructions for PR.") {
		t.Errorf("unexpected prompt: %q", cfg.Prompt)
	}

	// Strict parsing fails with unexpected fields.
	badPath := filepath.Join(tmpDir, "bad.yaml")
	badContent := `prompt: test
unknown_field: true
`
	if err := os.WriteFile(badPath, []byte(badContent), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	var badCfg Config
	if err := cmdyaml.ParseConfigFilesStrict(t.Context(), &badCfg, badPath); err == nil {
		t.Fatalf("expected error for unknown field in strict parse")
	}
}

func TestConfigPromptFor(t *testing.T) {
	cfg := Config{
		Prompt: "default prompt",
		Prompts: map[string]string{
			"claude": "claude specific prompt",
		},
	}
	if got, want := cfg.PromptFor("claude"), "claude specific prompt"; got != want {
		t.Errorf("claude: got %q, want %q", got, want)
	}
	if got, want := cfg.PromptFor("agy"), "default prompt"; got != want {
		t.Errorf("agy: got %q, want %q", got, want)
	}

	emptyCfg := Config{}
	if got, want := emptyCfg.PromptFor("claude"), defaultInstructions; got != want {
		t.Errorf("empty config: got %q, want defaultInstructions", got)
	}
}

func TestParseConfigFileWithPrompts(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	content := `
prompt: default prompt
prompts:
  claude: claude prompt
  agy: agy prompt
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	var cfg Config
	if err := cmdyaml.ParseConfigFilesStrict(t.Context(), &cfg, cfgPath); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate config: %v", err)
	}
	if got, want := cfg.Prompt, "default prompt"; got != want {
		t.Errorf("got Prompt %q, want %q", got, want)
	}
	if got, want := cfg.Prompts["claude"], "claude prompt"; got != want {
		t.Errorf("got claude prompt %q, want %q", got, want)
	}
	if got, want := cfg.Prompts["agy"], "agy prompt"; got != want {
		t.Errorf("got agy prompt %q, want %q", got, want)
	}
}

func TestParseConfigFileWithAnchor(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	content := `
common_prompt: &common_prompt |-
  Shared instructions across tools.

prompts:
  claude: *common_prompt
  agy: *common_prompt
`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	var cfg Config
	if err := cmdyaml.ParseConfigFilesStrict(t.Context(), &cfg, cfgPath); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate config: %v", err)
	}
	want := "Shared instructions across tools."
	if got := cfg.Prompts["claude"]; got != want {
		t.Errorf("claude: got %q, want %q", got, want)
	}
	if got := cfg.Prompts["agy"]; got != want {
		t.Errorf("agy: got %q, want %q", got, want)
	}
}

func TestConfigPrehook(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "my_prompt.yaml")
	if err := os.WriteFile(cfgPath, []byte("prompt: custom prompt text\n"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// 1. Explicitly set config that does not exist -> error
	t.Run("nonexistent explicit config", func(t *testing.T) {
		var gf GlobalFlags
		fs := subcmd.NewFlagSet().MustRegisterFlagStruct(&gf, nil, nil)
		if err := fs.FlagSet().Parse([]string{"--config", filepath.Join(tmpDir, "nonexistent.yaml")}); err != nil {
			t.Fatalf("parse flags: %v", err)
		}
		saved := globalFlags
		defer func() { globalFlags = saved }()
		globalFlags = gf

		ctx := subcmd.WithGlobalFlagSet(context.Background(), fs)
		_, _, _, err := configPrehook(ctx)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected 'not found' error, got %v", err)
		}
	})

	// 2. Explicitly set config that exists -> loads config
	t.Run("existing explicit config", func(t *testing.T) {
		var gf GlobalFlags
		fs := subcmd.NewFlagSet().MustRegisterFlagStruct(&gf, nil, nil)
		if err := fs.FlagSet().Parse([]string{"--config", cfgPath}); err != nil {
			t.Fatalf("parse flags: %v", err)
		}
		saved := globalFlags
		defer func() { globalFlags = saved }()
		globalFlags = gf

		ctx := subcmd.WithGlobalFlagSet(context.Background(), fs)
		outCtx, _, _, err := configPrehook(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfg, ok := cmdutil.ContextValue[Config](outCtx)
		if !ok {
			t.Fatalf("config not in context")
		}
		if got, want := cfg.Prompt, "custom prompt text"; got != want {
			t.Errorf("got prompt %q, want %q", got, want)
		}
		loc, ok := cmdutil.ContextValue[*cmdconfig.Located](outCtx)
		if !ok || len(loc.YAML) != 1 || loc.YAML[0] != cfgPath {
			t.Errorf("got files %v, want [%s]", loc, cfgPath)
		}
	})

	// 3. Unset config when no default file exists -> succeeds with default prompt
	t.Run("unset config", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("HOME", tmpDir)
		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		var gf GlobalFlags
		fs := subcmd.NewFlagSet().MustRegisterFlagStruct(&gf, nil, nil)
		if err := fs.FlagSet().Parse([]string{}); err != nil {
			t.Fatalf("parse flags: %v", err)
		}
		saved := globalFlags
		defer func() { globalFlags = saved }()
		globalFlags = gf

		ctx := subcmd.WithGlobalFlagSet(context.Background(), fs)
		outCtx, _, _, err := configPrehook(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cfg, ok := cmdutil.ContextValue[Config](outCtx)
		if !ok {
			t.Fatalf("expected config in context")
		}
		if cfg.Prompt != defaultInstructions {
			t.Errorf("expected default instructions, got %q", cfg.Prompt)
		}
		if loc, ok := cmdutil.ContextValue[*cmdconfig.Located](outCtx); ok && len(loc.YAML) > 0 {
			t.Errorf("expected no config files in context, got %v", loc.YAML)
		}
	})
}

func captureOutput(f func()) string {
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	outC := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		outC <- buf.String()
	}()

	f()
	_ = w.Close()
	return <-outC
}

func TestCLIConfigShow(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "test_config.yaml")
	if err := os.WriteFile(cfgPath, []byte("prompt: hello from test\n"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cli := createCLI()
	cli.WithGlobalFlags(subcmd.NewFlagSet().MustRegisterFlagStruct(&globalFlags, nil, nil))

	out := captureOutput(func() {
		err := cli.DispatchWithArgs(context.Background(), "ghpr", "--config", cfgPath, "config", "show")
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	})
	if !strings.Contains(out, "# Configuration file: "+cfgPath) {
		t.Errorf("missing config file path in output: %q", out)
	}
	if !strings.Contains(out, "hello from test") {
		t.Errorf("missing prompt in output: %q", out)
	}
}

func TestCLIConfigDescribe(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	globalFlags = GlobalFlags{}
	cli := createCLI()
	cli.WithGlobalFlags(subcmd.NewFlagSet().MustRegisterFlagStruct(&globalFlags, nil, nil))
	out := captureOutput(func() {
		if err := cli.DispatchWithArgs(context.Background(), "ghpr", "config", "describe"); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	})
	if !strings.Contains(out, "prompt") || !strings.Contains(out, "root configuration options") {
		t.Errorf("unexpected output: %q", out)
	}
}

// TestSearchPath covers the directories a configuration file is looked for in:
// ghpr's own, plus the current directory only when a file was named on the
// command line.
func TestSearchPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dirs := filepath.SplitList(searchPath(false))
	if len(dirs) == 0 {
		t.Fatal("no directories to search")
	}
	if slices.Contains(dirs, ".") {
		t.Errorf("the current directory is searched by default: %v", dirs)
	}

	explicit := filepath.SplitList(searchPath(true))
	if got, want := explicit[0], "."; got != want {
		t.Errorf("a named file is not resolved against the current directory first: got %v, want %v", got, want)
	}
	if got, want := explicit[1:], dirs; !slices.Equal(got, want) {
		t.Errorf("the rest of the path differs: got %v, want %v", got, want)
	}
}

// writeConfig writes a configuration file naming where it came from, so that a
// test can tell which of them was read.
func writeConfig(t *testing.T, dir, name, from string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir %v: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("prompt: "+from+"\n"), 0600); err != nil {
		t.Fatalf("write %v: %v", path, err)
	}
	return path
}

// prehookWithArgs runs configPrehook with args as the global flags, as a
// command dispatched with them would.
func prehookWithArgs(t *testing.T, args ...string) (Config, *cmdconfig.Located) {
	t.Helper()
	var gf GlobalFlags
	fs := subcmd.NewFlagSet().MustRegisterFlagStruct(&gf, nil, nil)
	if err := fs.FlagSet().Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	saved := globalFlags
	t.Cleanup(func() { globalFlags = saved })
	globalFlags = gf

	ctx, _, _, err := configPrehook(subcmd.WithGlobalFlagSet(context.Background(), fs))
	if err != nil {
		t.Fatalf("configPrehook: %v", err)
	}
	cfg, ok := cmdutil.ContextValue[Config](ctx)
	if !ok {
		t.Fatal("no configuration in context")
	}
	loc, _ := cmdutil.ContextValue[*cmdconfig.Located](ctx)
	return cfg, loc
}

// TestConfigPrehookCurrentDirectory covers ghpr being run in arbitrary
// repositories: a configuration file that happens to be in the current
// directory is almost certainly not ghpr's, so it is read only when it is
// asked for by name.
func TestConfigPrehookCurrentDirectory(t *testing.T) {
	t.Run("not read by default", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		cwd := t.TempDir()
		t.Chdir(cwd)
		writeConfig(t, cwd, "config.yaml", "the current directory")
		writeConfig(t, cwd, "ghpr.yaml", "the current directory")

		cfg, loc := prehookWithArgs(t)
		if cfg.Prompt != defaultInstructions {
			t.Errorf("a file in the current directory was read: %q", cfg.Prompt)
		}
		if loc != nil && len(loc.YAML) > 0 {
			t.Errorf("located %v", loc.YAML)
		}
	})

	t.Run("read when named", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		cwd := t.TempDir()
		t.Chdir(cwd)
		writeConfig(t, cwd, "config.yaml", "the current directory")

		// A relative name is resolved against the current directory, as it is
		// for any other command.
		cfg, loc := prehookWithArgs(t, "--config", "config.yaml")
		if got, want := cfg.Prompt, "the current directory"; got != want {
			t.Errorf("got prompt %q, want %q", got, want)
		}
		if loc == nil || len(loc.YAML) != 1 {
			t.Fatalf("located %v", loc)
		}
	})

	t.Run("the user configuration directory is still searched", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		cwd := t.TempDir()
		t.Chdir(cwd)
		writeConfig(t, cwd, "config.yaml", "the current directory")
		path := writeConfig(t, filepath.Join(home, ".config", appDir), "ghpr.yaml", "the user configuration directory")

		cfg, loc := prehookWithArgs(t)
		if got, want := cfg.Prompt, "the user configuration directory"; got != want {
			t.Errorf("got prompt %q, want %q", got, want)
		}
		if loc == nil || len(loc.YAML) != 1 || loc.YAML[0] != path {
			t.Errorf("located %v, want [%v]", loc, path)
		}
	})
}
