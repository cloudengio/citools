// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs an external command. Everything ghpr does to a repository or to
// GitHub goes through one, so that the commands can be tested without a
// repository, a network, or an agent.
type Runner interface {
	// Run runs name with args, writes stdin to its standard input when it is
	// not empty, and returns its standard output. The error reports the
	// command's standard error, since that is where git and gh say what went
	// wrong.
	Run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)
}

// execRunner runs commands with os/exec.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.Bytes(), fmt.Errorf("%v: %w: %v", commandLine(name, args), err, msg)
		}
		return stdout.Bytes(), fmt.Errorf("%v: %w", commandLine(name, args), err)
	}
	return stdout.Bytes(), nil
}

// commandLine renders a command for an error message or a dry run. It is for
// reading, not for re-running: an argument containing a space is quoted, but
// nothing is escaped.
func commandLine(name string, args []string) string {
	var sb strings.Builder
	sb.WriteString(name)
	for _, arg := range args {
		sb.WriteByte(' ')
		// An empty argument has to be quoted or it would vanish, leaving a
		// flag that takes a value looking as though it was given none.
		if arg == "" || strings.ContainsAny(arg, " \t\n") {
			fmt.Fprintf(&sb, "%q", arg)
			continue
		}
		sb.WriteString(arg)
	}
	return sb.String()
}

// formatCommand renders a command along with any standard input that would be
// fed to it, formatted as a shell command.
func formatCommand(stdin []byte, name string, args ...string) string {
	cmd := commandLine(name, args)
	if len(stdin) == 0 {
		return cmd
	}
	in := string(stdin)
	if !strings.HasSuffix(in, "\n") {
		in += "\n"
	}
	eof := "EOF"
	if strings.Contains(in, "\nEOF") {
		eof = "GHPR_DIFF_EOF"
	}
	return fmt.Sprintf("%v << '%s'\n%s%s", cmd, eof, in, eof)
}

// dryRunner reports what would be run without running it, and is used for
// --dry-run. Commands that only read are still run, since a dry run has to
// be able to say what it would have done: it is the Runner handed to the
// parts of ghpr that change something.
type dryRunner struct {
	announce func(string)
}

func (d dryRunner) Run(_ context.Context, _ []byte, name string, args ...string) ([]byte, error) {
	d.announce(commandLine(name, args))
	return nil, nil
}
