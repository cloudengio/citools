// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package internal_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
)

// newManager returns a LogFileManager rooted under the test's temp directory,
// which is removed when the test ends. NewLogFileManager creates its directory
// under the system temp directory, so TMPDIR is redirected for the call.
func newManager(t *testing.T) *internal.LogFileManager {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	lm, err := internal.NewLogFileManager("orchestrator-test")
	if err != nil {
		t.Fatalf("NewLogFileManager: %v", err)
	}
	t.Cleanup(lm.CloseGlobalLogFile)
	return lm
}

// closeFile closes f, reporting a failure that would otherwise go unnoticed.
func closeFile(t *testing.T, f *os.File) {
	t.Helper()
	if err := f.Close(); err != nil {
		t.Errorf("closing %v: %v", f.Name(), err)
	}
}

func TestLogFileManager(t *testing.T) {
	lm := newManager(t)

	// The global log file is a real, writable file, not the discard sink.
	w := lm.GlobalLogFile()
	if w == io.Discard {
		t.Fatal("GlobalLogFile returned io.Discard for an initialised manager")
	}
	if _, err := io.WriteString(w, "global\n"); err != nil {
		t.Errorf("writing to the global log file: %v", err)
	}
	f, ok := w.(*os.File)
	if !ok {
		t.Fatalf("GlobalLogFile returned %T, want *os.File", w)
	}
	lm.CloseGlobalLogFile()
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("reading %v: %v", f.Name(), err)
	}
	if got, want := strings.TrimSpace(string(data)), "global"; got != want {
		t.Errorf("global log file: got %q, want %q", got, want)
	}

	// Closing twice must not panic: Close is called both by the deferred
	// shutdown and by the handler's own Close.
	lm.CloseGlobalLogFile()
}

// TestCreateTempFilesForJob covers the naming the web UI and the failure
// dialogs rely on to attribute a log to a runner, and that the two files for a
// job are distinct.
func TestCreateTempFilesForJob(t *testing.T) {
	lm := newManager(t)

	logFile, diagFile, err := lm.CreateTempFilesForJob("go-macos-20260824-0001")
	if err != nil {
		t.Fatalf("CreateTempFilesForJob: %v", err)
	}
	defer closeFile(t, logFile)
	defer closeFile(t, diagFile)

	if logFile.Name() == diagFile.Name() {
		t.Fatal("the job and diagnostic logs are the same file")
	}
	for _, tc := range []struct {
		name             string
		file             *os.File
		wantMid, wantExt string
	}{
		{"job log", logFile, "-job-", ".txt"},
		{"diagnostic log", diagFile, "-diag-", ".tar.gz"},
	} {
		base := filepath.Base(tc.file.Name())
		if !strings.HasPrefix(base, "go-macos-20260824-0001") {
			t.Errorf("%v: %q does not start with the runner name", tc.name, base)
		}
		if !strings.Contains(base, tc.wantMid) {
			t.Errorf("%v: %q does not contain %q", tc.name, base, tc.wantMid)
		}
		if !strings.HasSuffix(base, tc.wantExt) {
			t.Errorf("%v: %q does not end with %q", tc.name, base, tc.wantExt)
		}
	}

	// Both files live alongside the global log, so a single directory holds
	// everything for a run.
	if got, want := filepath.Dir(logFile.Name()), filepath.Dir(diagFile.Name()); got != want {
		t.Errorf("the job logs are in different directories: %q and %q", got, want)
	}

	// A second job gets its own files rather than truncating the first.
	logFile2, diagFile2, err := lm.CreateTempFilesForJob("go-macos-20260824-0002")
	if err != nil {
		t.Fatalf("CreateTempFilesForJob: %v", err)
	}
	defer closeFile(t, logFile2)
	defer closeFile(t, diagFile2)
	if logFile2.Name() == logFile.Name() || diagFile2.Name() == diagFile.Name() {
		t.Error("a second job reused the first job's files")
	}

	// The same runner name twice, as happens on a retry, still yields distinct
	// files: the name is a prefix, not the whole name.
	again, _, err := lm.CreateTempFilesForJob("go-macos-20260824-0001")
	if err != nil {
		t.Fatalf("CreateTempFilesForJob: %v", err)
	}
	defer closeFile(t, again)
	if again.Name() == logFile.Name() {
		t.Error("re-using a runner name reused its log file")
	}
}

func TestCreateTemp(t *testing.T) {
	lm := newManager(t)

	f, err := lm.CreateTemp("runner", "step", ".log")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer closeFile(t, f)
	base := filepath.Base(f.Name())
	if !strings.HasPrefix(base, "runner-step-") || !strings.HasSuffix(base, ".log") {
		t.Errorf("CreateTemp produced %q, want runner-step-*.log", base)
	}
	if _, err := io.WriteString(f, "step output\n"); err != nil {
		t.Errorf("writing to the step log: %v", err)
	}
}

// TestGlobalLogFileUninitialised verifies the documented fallback: a manager
// with no log file discards writes rather than panicking, which is what keeps
// the orchestrator running when its log directory is unusable.
func TestGlobalLogFileUninitialised(t *testing.T) {
	var lm internal.LogFileManager
	if got := lm.GlobalLogFile(); got != io.Discard {
		t.Errorf("GlobalLogFile = %v, want io.Discard", got)
	}
	if _, err := io.WriteString(lm.GlobalLogFile(), "ignored\n"); err != nil {
		t.Errorf("writing to the discard sink: %v", err)
	}
	// Closing a manager that never opened a file is a no-op.
	lm.CloseGlobalLogFile()
}
