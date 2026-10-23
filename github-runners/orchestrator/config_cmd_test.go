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
	ctx := ContextWithConfig(context.Background(), cfg)
	ctx = ContextWithConfigFile(ctx, "/path/to/my_orchestrator.yml")

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
	ctx := ContextWithConfig(context.Background(), cfg)

	out := captureStdout(func() {
		if err := cmd.Show(ctx, nil, nil); err != nil {
			t.Fatalf("Show: %v", err)
		}
	})

	if !strings.Contains(out, "# Configuration file: none") {
		t.Errorf("expected '# Configuration file: none', got: %q", out)
	}
}
