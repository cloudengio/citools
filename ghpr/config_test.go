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
	"strings"
	"testing"

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

func TestConfigContext(t *testing.T) {
	if _, ok := ConfigFromContext(nil); ok { //nolint:staticcheck // testing nil context handling explicitly
		t.Errorf("got ok=true for nil context")
	}
	if _, ok := ConfigFromContext(context.Background()); ok {
		t.Errorf("got ok=true for context without config")
	}
	if _, ok := ConfigFilesFromContext(nil); ok { //nolint:staticcheck // testing nil context handling explicitly
		t.Errorf("got ok=true for nil context on ConfigFilesFromContext")
	}
	if _, ok := ConfigFilesFromContext(context.Background()); ok {
		t.Errorf("got ok=true for context without config files")
	}

	cfg := Config{Prompt: "test prompt"}
	ctx := ContextWithConfig(context.Background(), cfg)
	got, ok := ConfigFromContext(ctx)
	if !ok {
		t.Fatalf("got ok=false, want true")
	}
	if got.Prompt != cfg.Prompt {
		t.Errorf("got prompt %q, want %q", got.Prompt, cfg.Prompt)
	}

	ctx = ContextWithConfigFile(ctx, "/path/to/file.yaml")
	files, ok := ConfigFilesFromContext(ctx)
	if !ok || len(files) != 1 || files[0] != "/path/to/file.yaml" {
		t.Errorf("got files %v, want [/path/to/file.yaml]", files)
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
		cfg, ok := ConfigFromContext(outCtx)
		if !ok {
			t.Fatalf("config not in context")
		}
		if got, want := cfg.Prompt, "custom prompt text"; got != want {
			t.Errorf("got prompt %q, want %q", got, want)
		}
		files, ok := ConfigFilesFromContext(outCtx)
		if !ok || len(files) != 1 || files[0] != cfgPath {
			t.Errorf("got files %v, want [%s]", files, cfgPath)
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
		cfg, ok := ConfigFromContext(outCtx)
		if !ok {
			t.Fatalf("expected config in context")
		}
		if cfg.Prompt != defaultInstructions {
			t.Errorf("expected default instructions, got %q", cfg.Prompt)
		}
		if files, ok := ConfigFilesFromContext(outCtx); ok && len(files) > 0 {
			t.Errorf("expected no config files in context, got %v", files)
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

func TestConfigCommandShowAndDescribe(t *testing.T) {
	cmd := ConfigCommand{}

	t.Run("show with config file", func(t *testing.T) {
		ctx := ContextWithConfig(context.Background(), Config{Prompt: "prompt text\n"})
		ctx = ContextWithConfigFile(ctx, "/path/to/my_config.yaml")
		out := captureOutput(func() {
			if err := cmd.Show(ctx, nil, nil); err != nil {
				t.Fatalf("Show: %v", err)
			}
		})
		if !strings.Contains(out, "# Configuration file: /path/to/my_config.yaml") {
			t.Errorf("output missing config file line: %q", out)
		}
		if !strings.Contains(out, "prompt: |") {
			t.Errorf("output missing prompt: %q", out)
		}
	})

	t.Run("show without config file", func(t *testing.T) {
		ctx := ContextWithConfig(context.Background(), Config{Prompt: defaultInstructions})
		out := captureOutput(func() {
			if err := cmd.Show(ctx, nil, nil); err != nil {
				t.Fatalf("Show: %v", err)
			}
		})
		if !strings.Contains(out, "# Configuration file: none (using defaults)") {
			t.Errorf("output missing none indicator: %q", out)
		}
		if !strings.Contains(out, "senior software engineer") {
			t.Errorf("output missing prompt: %q", out)
		}
	})

	t.Run("describe", func(t *testing.T) {
		out := captureOutput(func() {
			if err := cmd.Describe(context.Background(), nil, nil); err != nil {
				t.Fatalf("Describe: %v", err)
			}
		})
		if !strings.Contains(out, "prompt") {
			t.Errorf("describe output missing prompt field: %q", out)
		}
	})
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
