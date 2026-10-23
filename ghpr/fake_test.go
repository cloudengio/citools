// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// fakeCall is one command a fakeRunner was asked to run.
type fakeCall struct {
	cmd   string // as commandLine renders it
	stdin string
}

// fakeRunner answers commands from a script keyed by the command line, and
// records what it was asked. A command that is not in the script is an error
// naming it, so that a test says which command it did not expect rather than
// failing somewhere further on.
type fakeRunner struct {
	mu      sync.Mutex
	script  map[string]fakeResult
	queue   map[string][]fakeResult // consumed in order, before script
	prefix  map[string]fakeResult   // matched by prefix, for commands with a generated argument
	calls   []fakeCall
	tb      testing.TB
	unknown []string
}

type fakeResult struct {
	out string
	err error
}

func newFakeRunner(tb testing.TB) *fakeRunner {
	return &fakeRunner{
		script: map[string]fakeResult{},
		queue:  map[string][]fakeResult{},
		prefix: map[string]fakeResult{},
		tb:     tb,
	}
}

// expectSeq scripts successive answers for one command line, which is how a
// retry is tested: the first call fails, a later one succeeds. Once the
// sequence is used up the command falls back to expect.
func (f *fakeRunner) expectSeq(cmd string, results ...fakeResult) *fakeRunner {
	f.queue[cmd] = append(f.queue[cmd], results...)
	return f
}

// expect scripts an exact command line.
func (f *fakeRunner) expect(cmd, out string, err error) *fakeRunner {
	f.script[cmd] = fakeResult{out: out, err: err}
	return f
}

// expectPrefix scripts any command line that starts with cmd, for one whose
// arguments are generated, such as a temporary file name.
func (f *fakeRunner) expectPrefix(cmd, out string, err error) *fakeRunner {
	f.prefix[cmd] = fakeResult{out: out, err: err}
	return f
}

func (f *fakeRunner) Run(_ context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := commandLine(name, args)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{cmd: cmd, stdin: string(stdin)})
	if q := f.queue[cmd]; len(q) > 0 {
		res := q[0]
		f.queue[cmd] = q[1:]
		return []byte(res.out), res.err
	}
	if res, ok := f.script[cmd]; ok {
		return []byte(res.out), res.err
	}
	for p, res := range f.prefix {
		if strings.HasPrefix(cmd, p) {
			return []byte(res.out), res.err
		}
	}
	f.unknown = append(f.unknown, cmd)
	return nil, fmt.Errorf("fakeRunner: no result scripted for %v", cmd)
}

// ran reports whether a command line was run.
func (f *fakeRunner) ran(cmd string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.cmd == cmd {
			return true
		}
	}
	return false
}

// ranPrefix reports whether any command line starting with prefix was run.
func (f *fakeRunner) ranPrefix(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if strings.HasPrefix(call.cmd, prefix) {
			return true
		}
	}
	return false
}

// stdinFor returns the standard input given to the first matching command.
func (f *fakeRunner) stdinFor(prefix string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if strings.HasPrefix(call.cmd, prefix) {
			return call.stdin
		}
	}
	return ""
}

func (f *fakeRunner) ranCommands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var cmds []string
	for _, call := range f.calls {
		cmds = append(cmds, call.cmd)
	}
	return cmds
}

// assertNoUnexpected fails the test if any command was run that was not
// scripted, which is usually a test that has fallen behind the code.
func (f *fakeRunner) assertNoUnexpected() {
	f.tb.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.unknown) > 0 {
		f.tb.Errorf("unexpected commands: %v", strings.Join(f.unknown, "; "))
	}
}

// testApp returns an App wired to run, with its output captured.
func testApp(tb testing.TB, run Runner, answer string) (*App, *bytes.Buffer) {
	tb.Helper()
	out := &bytes.Buffer{}
	git, gh := NewGit(run), NewGH(run)
	gh.retryDelay = 0 // A test should not wait out a real backoff.
	return &App{
		Git:  git,
		GH:   gh,
		Base: "main",
		Summarizer: &Summarizer{
			git:          git,
			run:          run,
			engine:       "claude",
			maxDiffLines: 3000,
			log:          func(string, ...any) {},
		},
		Stdout: out,
		Stdin:  strings.NewReader(answer),
	}, out
}

// diffCmd is the command the summarizer takes a diff with, which every test
// that reaches the agent has to script.
func diffCmd(from, to string) string {
	return commandLine("git", append([]string{"diff", "-U3", from + ".." + to, "--", "."}, excludedPaths...))
}
