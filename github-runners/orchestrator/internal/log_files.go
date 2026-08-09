// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package internal

import (
	"fmt"
	"io"
	"os"
)

// LogFileManager manages log files for the orchestrator.
type LogFileManager struct {
	dir     string
	logFile *os.File
}

// NewLogFileManager creates a new LogFileManager with a temporary directory
// and a global log file.
func NewLogFileManager(dir string) (*LogFileManager, error) {
	dir, err := os.MkdirTemp("", dir)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	logFile, err := os.CreateTemp(dir, "vmspool-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	return &LogFileManager{
		dir:     dir,
		logFile: logFile,
	}, nil
}

// GlobalLogFile returns the global log file writer. If the log file is not
// initialized, it returns io.Discard.
func (l *LogFileManager) GlobalLogFile() io.Writer {
	if l.logFile == nil {
		return io.Discard
	}
	return l.logFile
}

// CloseGlobalLogFile closes the global log file if it is was ever initialized.
func (l *LogFileManager) CloseGlobalLogFile() {
	if l.logFile != nil {
		_ = l.logFile.Close()
	}
}

// CreateTemp creates a temporary file in the log file manager's directory with the specified
// runner name, step, and extension.
func (l *LogFileManager) CreateTemp(runnerName, step, ext string) (*os.File, error) {
	logFile, err := os.CreateTemp(l.dir, runnerName+"-"+step+"-*"+ext)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	return logFile, nil
}

// CreateTempFilesForJob creates two temporary files for a job: one for the job log
// and one for the diagnostic log.
func (l *LogFileManager) CreateTempFilesForJob(runnerName string) (logFile, diagLogFile *os.File, err error) {
	logFile, err = l.CreateTemp(runnerName, "job", ".txt")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp log file: %w", err)
	}
	diagLogFile, err = l.CreateTemp(runnerName, "diag", ".tar.gz")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp diag log file: %w", err)
	}
	return logFile, diagLogFile, nil
}
