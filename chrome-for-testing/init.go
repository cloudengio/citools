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

func (b browser) init(ctx context.Context) error {
	now := time.Now()
	stderr, stdout := &bytes.Buffer{}, &bytes.Buffer{}
	cmd := exec.CommandContext(ctx, b.binaryPath, append(initArgs,
		"--user-data-dir="+b.userDataDir,
		"about:blank",
	)...)
	if b.debug {
		cmd.Stdout = io.MultiWriter(stdout, os.Stdout)
		cmd.Stderr = io.MultiWriter(stderr, os.Stderr)
	} else {
		cmd.Stdout = stdout
		cmd.Stderr = stderr
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %v: %w", strings.Join(cmd.Args, " "), err)
	}
	profileDir := filepath.Join(b.userDataDir, "Default")
	if !b.waitForProfile(ctx, profileDir) {
		ctxlog.Info(ctx, "browser profile has not been initialized", "profile_dir", profileDir, "time_taken", time.Since(now).String())
		return nil
	}
	return terminateChromeProcesses(ctx, cmd, b.binaryPath, b.debug)
}

func (b browser) waitForProfile(ctx context.Context, profileDir string) bool {
	start := time.Now()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			ctxlog.Info(ctx, "timed out waiting for profile dir", "profile_dir", profileDir, "after", time.Since(start).String(), "error", ctx.Err())
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
