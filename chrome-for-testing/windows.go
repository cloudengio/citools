// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build windows

package main

import (
	"context"
	"fmt"
	"strings"

	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/windows/powershell"
)

func logError(ctx context.Context, msg, stdout, stderr string, args []string, err error) {
	ctxlog.Info(ctx, msg, "command line", strings.Join(args, " "), "error", err)
	ctxlog.Info(ctx, msg, "stdout", stdout)
	ctxlog.Info(ctx, msg, "stderr", stderr)
}

func prepareInstallDir(ctx context.Context, dir string) error {
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
	stdout, stderr, err := powershell.New().KillByPath(ctx, binaryPath)
	if err != nil {
		logError(ctx, "failed to terminate process by path", stdout, stderr, []string{binaryPath}, err)
		return fmt.Errorf("failed to terminate process by path %v: %w", binaryPath, err)
	}
	ctxlog.Info(ctx, "terminated process by path", "binary_path", binaryPath)
	return nil
}
