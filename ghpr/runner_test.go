// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
)

func TestCommandLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no arguments", nil, "git"},
		{"plain arguments", []string{"status", "--short"}, "git status --short"},
		// An empty argument must not vanish: --allowedTools "" is how the
		// agent is denied tools, and a rendering that drops it reads as
		// though the flag was given nothing.
		{"empty argument", []string{"--allowedTools", ""}, `git --allowedTools ""`},
		{"argument with spaces", []string{"-m", "a message"}, `git -m "a message"`},
		{"argument with a newline", []string{"-m", "a\nb"}, `git -m "a\nb"`},
		{"argument with a tab", []string{"-m", "a\tb"}, `git -m "a\tb"`},
		// Nothing else is escaped: this is for reading, not for re-running.
		{"argument with a quote", []string{`a"b`}, `git a"b`},
	} {
		if got := commandLine("git", tc.args); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestExecRunner(t *testing.T) {
	var r execRunner

	// Standard output is returned.
	out, err := r.Run(t.Context(), nil, "echo", "hello")
	if err != nil {
		t.Fatalf("echo: %v", err)
	}
	if got, want := strings.TrimSpace(string(out)), "hello"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Standard input is passed through.
	out, err = r.Run(t.Context(), []byte("from stdin"), "cat")
	if err != nil {
		t.Fatalf("cat: %v", err)
	}
	if got, want := string(out), "from stdin"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExecRunnerErrors(t *testing.T) {
	var r execRunner

	// The command's standard error is what says what went wrong, so it has
	// to reach the caller, along with the command that produced it.
	_, err := r.Run(t.Context(), nil, "sh", "-c", "echo the reason >&2; exit 3")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"sh -c", "the reason", "exit status 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}

	// A command that fails silently still reports which command it was.
	_, err = r.Run(t.Context(), nil, "sh", "-c", "exit 4")
	if err == nil || !strings.Contains(err.Error(), "exit status 4") {
		t.Errorf("got %v, want the exit status", err)
	}

	// A command that is not installed.
	if _, err := r.Run(t.Context(), nil, "ghpr-no-such-command"); err == nil {
		t.Error("expected an error for a command that does not exist")
	}
}

func TestDryRunner(t *testing.T) {
	var announced []string
	d := dryRunner{announce: func(cmd string) { announced = append(announced, cmd) }}

	// Nothing is run, and what would have been is reported. The output is
	// empty rather than invented, which is why only writes go through it.
	out, err := d.Run(t.Context(), []byte("ignored"), "git", "push", "origin", "main")
	if err != nil {
		t.Errorf("got %v, want no error", err)
	}
	if len(out) != 0 {
		t.Errorf("got %q, want no output", out)
	}
	if len(announced) != 1 || announced[0] != "git push origin main" {
		t.Errorf("got %v, want the command to be announced", announced)
	}
}
