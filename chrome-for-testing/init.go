// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/os/executil"
)

var initArgs = []string{
	"--headless=new",
	"--disable-gpu",
	"--no-sandbox",
	"--remote-debugging-port=9222",
	"--no-default-browser-check",
}

type browser struct {
	goos        string
	binaryPath  string
	userDataDir string
	debug       bool
}

func (b browser) init(ctx context.Context, timeout time.Duration) error {
	stderr, stdout := &bytes.Buffer{}, &bytes.Buffer{}
	cmd := exec.CommandContext(ctx, b.binaryPath, append(initArgs,
		"--user-data-dir="+b.userDataDir,
		"about:blank",
	)...)
	if b.debug {
		cmd.Stderr = io.MultiWriter(stderr, os.Stderr)
		cmd.Stdout = io.MultiWriter(stdout, os.Stdout)
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %v: %w", strings.Join(cmd.Args, " "), err)
	}
	profileDir := filepath.Join(b.userDataDir, "Default")
	if !b.waitForProfile(ctx, profileDir, timeout) {
		ctxlog.Info(ctx, "browser profile has not been initialized", "profile_dir", profileDir)
		return nil
	}
	pid := cmd.Process.Pid
	ctxlog.Info(ctx, "terminating browser process after profile init timeout", "pid", pid, "profile_dir", profileDir, "timeout", timeout.String())
	err := executil.SignalAndWait(ctx, time.Second, cmd, os.Interrupt, os.Kill)
	if err != nil {
		ctxlog.Info(ctx, "failed to terminate browser process", "command", strings.Join(cmd.Args, " "), "error", err)
	}
	if err == nil && executil.IsStopped(pid) {
		return nil
	}
	ctxlog.Info(ctx, "browser process still running after termination attempt", "pid", pid)
	ctxlog.Info(ctx, "attempting to terminate browser process by binary path", "binary_path", b.binaryPath)
	if err := terminateProcessByPath(ctx, b.debug, b.binaryPath); err != nil {
		ctxlog.Info(ctx, "failed tp terminate browser process by binary path", "binary_path", b.binaryPath)
		return err
	}
	if !executil.IsStopped(pid) {
		return fmt.Errorf("browser process %d and path %v still running after multiple termination attempts", pid, b.binaryPath)

	}
	return nil
}

func (b browser) waitForProfile(ctx context.Context, profileDir string, timeout time.Duration) bool {
	ticker := time.NewTicker(1 * time.Second)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			ctxlog.Info(ctx, "timed out waiting for profile dir", "profile_dir", profileDir, "after", timeout.String(), "error", ctx.Err())
			return false
		case <-ticker.C:
			fi, err := os.Stat(profileDir)
			if err == nil {
				if !fi.IsDir() {
					ctxlog.Info(ctx, "profile dir is not a directory", "profile_dir", profileDir)
					return false
				}
				return true
			}
			if !os.IsNotExist(err) {
				ctxlog.Info(ctx, "error checking for profile dir", "profile_dir", profileDir, "error", err)
				continue
			}
			ctxlog.Debug(ctx, "waiting for profile dir", "profile_dir", profileDir, "error", err)
		}
	}
}
