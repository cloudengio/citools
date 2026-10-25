// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"cloudeng.io/cmdutil"
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

func TestConfigCommandShow(t *testing.T) {
	cmd := ConfigCommand{}
	cfg := Config{}
	ctx := cmdutil.ContextWithValue(context.Background(), cfg)
	ctx = cmdutil.ContextWithValue(ctx, ConfigFiles{"/path/to/my_orchestrator.yml"})

	out := captureStdout(func() {
		if err := cmd.Show(ctx, nil, nil); err != nil {
			t.Fatalf("Show: %v", err)
		}
	})

	if !strings.Contains(out, "# Configuration file: /path/to/my_orchestrator.yml") {
		t.Errorf("missing config file path in output: %q", out)
	}
}

func TestConfigCommandShowNoFile(t *testing.T) {
	cmd := ConfigCommand{}
	cfg := Config{}
	ctx := cmdutil.ContextWithValue(context.Background(), cfg)

	out := captureStdout(func() {
		if err := cmd.Show(ctx, nil, nil); err != nil {
			t.Fatalf("Show: %v", err)
		}
	})

	if !strings.Contains(out, "# Configuration file: none") {
		t.Errorf("expected '# Configuration file: none', got: %q", out)
	}
}

func TestConfigCommandShowWithEnvFile(t *testing.T) {
	cmd := ConfigCommand{}
	cfg := Config{}
	ctx := cmdutil.ContextWithValue(context.Background(), cfg)
	ctx = cmdutil.ContextWithValue(ctx, ConfigFiles{"/path/to/my_orchestrator.yml"})
	ctx = cmdutil.ContextWithValue(ctx, EnvConfigFile("/path/to/my_orchestrator.env"))

	out := captureStdout(func() {
		if err := cmd.Show(ctx, nil, nil); err != nil {
			t.Fatalf("Show: %v", err)
		}
	})

	if !strings.Contains(out, "# Configuration file: /path/to/my_orchestrator.yml") {
		t.Errorf("missing config file path in output: %q", out)
	}
	if !strings.Contains(out, "# Environment variables file: /path/to/my_orchestrator.env") {
		t.Errorf("missing env file path in output: %q", out)
	}
}
