// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// errAlreadyRunning is returned when another orchestrator already holds the run
// lock.
var errAlreadyRunning = errors.New("another orchestrator instance is already running")

// runLockPath returns the path of the single-instance run lock, in the per-user
// application-support directory. It is independent of --config so that only one
// orchestrator runs per user, regardless of how it was launched.
func runLockPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "github-runner-orchestrator", "run.lock"), nil
}

// acquireRunLock takes an exclusive, non-blocking advisory lock so that only one
// orchestrator runs at a time. The returned file must be kept open for the
// lifetime of the run; the lock is released automatically when the process exits
// — including on crash or SIGKILL — so it never goes stale. If another instance
// already holds the lock, errAlreadyRunning is returned (annotated with the
// holder's PID and the lock path).
func acquireRunLock() (*os.File, error) {
	path, err := runLockPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder := lockHolder(f)
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w%s; stop it before starting another (lock: %s)", errAlreadyRunning, holder, path)
		}
		return nil, fmt.Errorf("acquiring run lock %s: %w", path, err)
	}
	// Record our PID for diagnostics (best effort).
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return f, nil
}

// lockHolder returns a " (pid N)" suffix if the lock file records a PID.
func lockHolder(f *os.File) string {
	buf := make([]byte, 32)
	n, _ := f.ReadAt(buf, 0)
	pid := strings.TrimSpace(string(buf[:max(n, 0)]))
	if pid == "" {
		return ""
	}
	return " (pid " + pid + ")"
}
