// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloudeng.io/cmdutil"
	cerrors "cloudeng.io/errors"
)

func TestValidateListenAddress(t *testing.T) {
	valid := []string{
		"127.0.0.1:8088",
		"127.0.0.1",
		"localhost:8088",
		"localhost",
	}
	for _, addr := range valid {
		if err := validateListenAddress(addr); err != nil {
			t.Errorf("validateListenAddress(%q) returned error: %v, want nil", addr, err)
		}
	}

	invalid := []string{
		":8088",
		"0.0.0.0:8088",
		"0.0.0.0",
		"192.168.1.1:8088",
		"8.8.8.8:8088",
		"",
	}
	for _, addr := range invalid {
		if err := validateListenAddress(addr); err == nil {
			t.Errorf("validateListenAddress(%q) succeeded, want error", addr)
		}
	}
}

func TestLogDirOrDefault(t *testing.T) {
	home, _ := os.UserHomeDir()
	tests := []struct {
		input string
		want  string
	}{
		{"", filepath.Join(home, "Library", "Logs")},
		{"~", home},
		{"~/test_logs", filepath.Join(home, "test_logs")},
		{"/var/log", "/var/log"},
	}

	for _, tc := range tests {
		cfg := LaunchAgentConfig{LogDir: tc.input}
		got := cfg.LogDirOrDefault()
		if !strings.HasPrefix(got, "/") {
			t.Errorf("LogDirOrDefault(%q) = %q; want absolute path", tc.input, got)
		}
		if home != "" && got != tc.want {
			t.Errorf("LogDirOrDefault(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestIsCleanShutdown(t *testing.T) {
	if !isCleanShutdown(nil) {
		t.Errorf("isCleanShutdown(nil) = false, want true")
	}

	if !isCleanShutdown(context.Canceled) {
		t.Errorf("isCleanShutdown(context.Canceled) = false, want true")
	}

	if !isCleanShutdown(cmdutil.ErrInterrupt) {
		t.Errorf("isCleanShutdown(cmdutil.ErrInterrupt) = false, want true")
	}

	wrapped := fmt.Errorf("wrapped: %w", context.Canceled)
	if !isCleanShutdown(wrapped) {
		t.Errorf("isCleanShutdown(wrapped context.Canceled) = false, want true")
	}

	var m cerrors.M
	m.Append(context.Canceled)
	if !isCleanShutdown(m.Err()) {
		t.Errorf("isCleanShutdown(errors.M with context.Canceled) = false, want true")
	}

	realErr := errors.New("something crashed")
	if isCleanShutdown(realErr) {
		t.Errorf("isCleanShutdown(realErr) = true, want false")
	}

	var mixed cerrors.M
	mixed.Append(realErr)
	mixed.Append(context.Canceled)
	if isCleanShutdown(mixed.Err()) {
		t.Errorf("isCleanShutdown(mixed) = true, want false")
	}
}

