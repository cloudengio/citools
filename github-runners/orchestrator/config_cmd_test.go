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

	"cloudeng.io/cmdutil/subcmd"
)

func captureStdout(f func()) string {
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

// TestCLIConfigCommands runs the config commands, which are provided by
// cmdconfig.ConfigExtension, through the command line, with the configuration
// that the config pre-hook loads.
func TestCLIConfigCommands(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(cfgPath, minimalConfigYAML, 0o600); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, "test.env")
	if err := os.WriteFile(envPath, []byte("UNUSED=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	globalFlags = GlobalFlags{}
	cli := createCLI()
	cli.WithGlobalFlags(subcmd.NewFlagSet().MustRegisterFlagStruct(&globalFlags, nil, nil))

	out := captureStdout(func() {
		err := cli.DispatchWithArgs(context.Background(), "orchestrator", "--config", cfgPath+","+envPath, "config", "show")
		if err != nil {
			t.Fatalf("config show: %v", err)
		}
	})
	for _, want := range []string{"# Configuration file: " + cfgPath, "# Environment variables file: " + envPath, "webhook:"} {
		if !strings.Contains(out, want) {
			t.Errorf("config show output does not contain %q: %q", want, out)
		}
	}

	out = captureStdout(func() {
		if err := cli.DispatchWithArgs(context.Background(), "orchestrator", "--config", cfgPath, "config", "describe"); err != nil {
			t.Fatalf("config describe: %v", err)
		}
	})
	if !strings.Contains(out, "root configuration options") || !strings.Contains(out, "webhook") {
		t.Errorf("unexpected config describe output: %q", out)
	}
}
