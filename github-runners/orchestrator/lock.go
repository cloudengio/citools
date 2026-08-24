// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"path/filepath"

	"cloudeng.io/os/executil"

	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
)

// acquireRunLock takes a non-blocking, exclusive file lock so that only one
// orchestrator runs at a time. The returned unlock function must be called when
// the run finishes; the lock is also released automatically when the process
// exits — including on crash or SIGKILL — so it never goes stale. If another
// instance already holds the lock, errAlreadyRunning is returned, annotated with
// the holder's PID and the lock path.
//
// TryOpenFile is used rather than Mutex.TryLock so that the PID can be recorded
// through the locked handle itself. The Mutex's additional in-process guard is
// not needed: only RunCommand.Run acquires this lock, once per process.
func acquireRunLock() (unlock func(), err error) {
	path, err := executil.UserConfigDirPath(filepath.Join(internal.ConfigDir, internal.RunLockName))
	if err != nil {
		return nil, err
	}
	return executil.AcquireRunLock(path)
}
