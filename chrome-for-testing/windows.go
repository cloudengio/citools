// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build windows

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/windows/powershell"
)

func logError(ctx context.Context, msg, stdout, stderr string, args []string, err error) {
	ctxlog.Info(ctx, msg, "command line", strings.Join(args, " "), "error", err)
	ctxlog.Debug(ctx, msg, "stdout", stdout)
	ctxlog.Debug(ctx, msg, "stderr", stderr)
}

func prepareInstallDir(ctx context.Context, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pwsh := powershell.New()
	args := []string{"icacls", dir, "/grant", "'ALL APPLICATION PACKAGES:(OI)(CI)(RX)'", "/grant", "'ALL RESTRICTED APPLICATION PACKAGES:(OI)(CI)(RX)'",
		"/T", "/C"}
	ctxlog.Debug(ctx, "configuring sandbox permissions", "command", strings.Join(args, " "))
	stdout, stderr, err := pwsh.Run(ctx, args...)
	if err != nil {
		logError(ctx, "failed to configure sandbox permissions", stdout, stderr, args, err)
		return fmt.Errorf("failed to configure sandbox permissions for %v: %w", dir, err)
	}
	ctxlog.Info(ctx, "configured sandbox permissions", "dir", dir)
	return nil
}

func getVersion(ctx context.Context, debug bool, binaryPath string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pwsh := powershell.New()
	psCommand := fmt.Sprintf(`(Get-Item "%s").VersionInfo.ProductVersion`, binaryPath)
	args := []string{"-NoProfile", "-Command", psCommand}
	stdout, stderr, err := pwsh.Run(ctx, args...)
	if err != nil {
		logError(ctx, "failed to get version info", stdout, stderr, args, err)
		return "", fmt.Errorf("failed to get version info for %v: %w", binaryPath, err)
	}
	ctxlog.Info(ctx, "got version info", "binary", binaryPath, "version", strings.TrimSpace(stdout))
	return strings.TrimSpace(stdout), nil
}

func terminateProcessByPath(ctx context.Context, debug bool, binaryPath string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pwsh := powershell.New()
	args := []string{
		"Get-Process", "chrome", "-ErrorAction", "SilentlyContinue", "|",
		"Where-Object", "{", "$_.Path", "-eq", `"` + binaryPath + `"`, "}", "|",
		"Stop-Process", "-Force"}

	stdout, stderr, err := pwsh.Run(ctx, args...)
	if err != nil {
		logError(ctx, "failed to terminate processes", stdout, stderr, args, err)
		return fmt.Errorf("failed to terminate processes %v: %w", binaryPath, err)
	}
	if debug {
		ctxlog.Info(ctx, "terminated processes", "process_name", binaryPath)
	}
	return nil
}
