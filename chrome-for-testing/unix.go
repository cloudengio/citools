// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"cloudeng.io/logging/ctxlog"
)

func prepareInstallDir(_ context.Context, _ string) error {
	return nil
}

func getVersion(ctx context.Context, debug bool, binaryPath string) (string, error) {
	args := []string{"--version"}
	ctxlog.Debug(ctx, "running", "binary", binaryPath, "args", args)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	if debug {
		cmd.Stderr = io.MultiWriter(stderr, os.Stderr)
		cmd.Stdout = io.MultiWriter(stdout, os.Stdout)
	} else {
		cmd.Stderr = stderr
		cmd.Stdout = stdout
	}
	err := cmd.Run()
	if err != nil {
		ctxlog.Debug(ctx, "command stdout", "stdout", stdout.String())
		ctxlog.Debug(ctx, "command stderr", "stderr", stderr.String())
		return "", fmt.Errorf("running %v: %w", strings.Join(cmd.Args, " "), err)
	}
	return string(bytes.TrimSpace(stdout.Bytes())), nil
}

func terminateProcessByPath(ctx context.Context, debug bool, binaryPath string) error {
	return nil
}
