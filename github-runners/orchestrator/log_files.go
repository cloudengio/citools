// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"os"
)

type logFileManager struct {
	dir     string
	logFile *os.File
}

func newLogFileManager(dir string) (*logFileManager, error) {
	dir, err := os.MkdirTemp("", dir)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	logFile, err := os.CreateTemp(dir, "vmspool-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	fmt.Printf("created temp log file: %s\n", logFile.Name())
	return &logFileManager{
		dir:     dir,
		logFile: logFile,
	}, nil
}

func (l *logFileManager) Close() {
	if l.logFile != nil {
		_ = l.logFile.Close()
	}
}

func (l *logFileManager) CreateTemp(id string) io.Writer {
	if l.logFile == nil {
		return io.Discard
	}
	return l.logFile
}

func (l *logFileManager) createJobTemp(runnerName, step, ext string) (*os.File, string, error) {
	logFile, err := os.CreateTemp(l.dir, runnerName+"-"+step+"-*"+ext)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp file: %w", err)
	}
	return logFile, logFile.Name(), nil
}
