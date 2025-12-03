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

func prepareInstallDir(ctx context.Context, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pwsh := powershell.New()
	args := []string{"icacls", dir, "/grant", "ALL APPLICATION PACKAGES:(OI)(CI)(RX)", "/grant", "ALL RESTRICTED APPLICATION PACKAGES:(OI)(CI)(RX)",
		"/T", "/C"}
	ctxlog.Debug(ctx, "configuring sandbox permissions", "command", strings.Join(args, " "))
	stdout, stderr, err := pwsh.Run(ctx, args...)
	if err != nil {
		ctxlog.Info(ctx, "failed to configure sandbox permissions", "dir", dir, "command", strings.Join(args, " "), "error", err)
		ctxlog.Debug(ctx, "command stdout", "stdout", stdout)
		ctxlog.Debug(ctx, "command stderr", "stderr", stderr)
		return fmt.Errorf("failed to configure sandbox permissions for %v: %w", dir, err)
	}
	ctxlog.Info(ctx, "configured sandbox permissions", "dir", dir)
	return nil
}
