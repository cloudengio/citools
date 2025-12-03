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
	"time"

	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/os/executil"
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

func terminateChromeProcesses(ctx context.Context, cmd *exec.Cmd, binaryPath string, debug bool) error {
	pid := cmd.Process.Pid
	ctxlog.Info(ctx, "terminating browser process by pid", "pid", pid)
	err := executil.SignalAndWait(ctx, time.Second, cmd, os.Interrupt, os.Kill)
	if err != nil {
		ctxlog.Info(ctx, "failed to terminate browser process by pid", "command", strings.Join(cmd.Args, " "), "error", err)
		return fmt.Errorf("failed to terminate browser process with pid %v: %w", pid, err)
	}
	if executil.IsStopped(pid) {
		ctxlog.Info(ctx, "browser process stopped", "pid", pid)
		return nil
	}
	return fmt.Errorf("browser process with pid %v still running", pid)
}
