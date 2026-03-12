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
	"--disable-crash-reporter",
}

type browser struct {
	goos        string
	binaryPath  string
	userDataDir string
	nssDir      string
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
	for _, dir := range []string{
		"Default",
		filepath.Join("Default", "ClientCertificates"),
		filepath.Join("shared_proto_db", "metadata"),
	} {
		dir := filepath.Join(b.userDataDir, dir)
		if !b.waitForDir(ctx, dir) {
			ctxlog.Info(ctx, "browser profile has not been initialized", "profile_dir", dir, "time_taken", time.Since(now).String())
			return nil
		}
	}
	if len(b.nssDir) > 0 {
		if !b.waitForDir(ctx, b.nssDir) {
			ctxlog.Info(ctx, "nss dir has not been initialized", "nss_dir", b.nssDir, "time_taken", time.Since(now).String())
			return nil
		}
	}
	return terminateChromeProcesses(ctx, cmd, b.binaryPath, b.debug)
}

func (b browser) waitForDir(ctx context.Context, dir string) bool {
	start := time.Now()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			ctxlog.Info(ctx, "timed out waiting for dir", "dir", dir, "after", time.Since(start).String(), "error", ctx.Err())
			return false
		case <-ticker.C:
			fi, err := os.Stat(dir)
			if err == nil {
				if !fi.IsDir() {
					ctxlog.Info(ctx, "dir is not a directory", "dir", dir)
					return false
				}
				return true
			}
			if !os.IsNotExist(err) {
				ctxlog.Info(ctx, "error checking for dir", "dir", dir, "error", err)
				continue
			}
			ctxlog.Debug(ctx, "waiting for dir", "dir", dir, "error", err)
		}
	}
}
