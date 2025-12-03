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

// "--user-data-dir=$USERDATA_DIR", "about:blank"

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
	if b.waitForProfile(ctx, profileDir, timeout) {
		ctxlog.Info(ctx, "browser profile initialized", "profile_dir", profileDir)
		return nil
	}
	pid := cmd.Process.Pid
	ctxlog.Info(ctx, "terminating browser process after profile init timeout", "pid", pid, "profile_dir", profileDir, "timeout", timeout.String())
	err := executil.SignalAndWait(ctx, time.Second, cmd, os.Interrupt, os.Kill)
	if err != nil {
		ctxlog.Info(ctx, "failed to terminate browser process", "command", strings.Join(cmd.Args, " "), "error", err)
	}
	if !executil.IsStopped(pid) {
		ctxlog.Info(ctx, "browser process still running after termination attempt", "pid", pid)
	}
	lockFile := filepath.Join(profileDir, "SingletonLock")
	ctxlog.Info(ctx, "waiting for browser lock file removal", "lock_file", lockFile)
	if !b.waitForLockFileRemoval(ctx, lockFile, timeout) {
		return fmt.Errorf("browser lock file %q still present after timeout", lockFile)
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

func (b browser) waitForLockFileRemoval(ctx context.Context, lockFile string, timeout time.Duration) bool {
	ticker := time.NewTicker(1 * time.Second)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			ctxlog.Info(ctx, "timed out waiting for lock file removal", "lock_file", lockFile, "after", timeout.String(), "error", ctx.Err())
			return false
		case <-ticker.C:
			_, err := os.Stat(lockFile)
			if os.IsNotExist(err) {
				return true
			}
			if err != nil {
				ctxlog.Info(ctx, "error checking for lock file", "lock_file", lockFile, "error", err)
				continue
			}
			ctxlog.Debug(ctx, "waiting for lock file removal", "lock_file", lockFile)
		}
	}
}
