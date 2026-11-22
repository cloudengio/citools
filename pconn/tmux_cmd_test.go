// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloudeng.io/net/pssh"
	"golang.org/x/crypto/ssh"
)

func TestTmuxCommandLine(t *testing.T) {
	for _, tc := range []struct {
		session string
		control bool
		want    string
	}{
		{"main", true, `'tmux' '-CC' 'new' '-A' '-s' 'main'`},
		{"main", false, `'tmux' 'new' '-A' '-s' 'main'`},
		{"it's mine", true, `'tmux' '-CC' 'new' '-A' '-s' 'it'\''s mine'`},
	} {
		if got := tmuxCommandLine(tc.session, tc.control); got != tc.want {
			t.Errorf("%q, %v: got %s, want %s", tc.session, tc.control, got, tc.want)
		}
	}
}

func TestConnectionLost(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&ssh.ExitMissingError{}, true},
		{fmt.Errorf("wrapped: %w", io.EOF), true},
		{&ssh.ExitError{Waitmsg: ssh.Waitmsg{}}, false},
		{errors.New("something else"), false},
		{nil, false},
	} {
		if got := connectionLost(tc.err); got != tc.want {
			t.Errorf("%v: got %v, want %v", tc.err, got, tc.want)
		}
	}
}

// fakeClient plays back a script of results for RunInteractive.
type fakeClient struct {
	mu        sync.Mutex
	results   []func(ctx context.Context) error
	calls     int
	waits     int
	waitErr   error
	connected chan struct{}
}

func (f *fakeClient) WaitConnected(ctx context.Context) error {
	f.mu.Lock()
	f.waits++
	ch, err := f.connected, f.waitErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeClient) RunInteractive(ctx context.Context, _ string, _ ...pssh.RunOption) error {
	f.mu.Lock()
	i := f.calls
	f.calls++
	f.mu.Unlock()
	if i >= len(f.results) {
		return errors.New("unexpected call")
	}
	return f.results[i](ctx)
}

func result(err error) func(context.Context) error {
	return func(context.Context) error { return err }
}

type attachFixture struct {
	a        *attachment
	statuses []string
	raws     atomic.Int32
	restores atomic.Int32
}

func newAttachFixture(c attacher) *attachFixture {
	f := &attachFixture{}
	f.a = &attachment{
		client:     c,
		name:       "host",
		cmd:        "tmux",
		attached:   &atomic.Bool{},
		in:         strings.NewReader(""),
		out:        io.Discard,
		retryDelay: time.Millisecond,
		status:     func(format string, args ...any) { f.statuses = append(f.statuses, fmt.Sprintf(format, args...)) },
		raw: func() (func(), error) {
			f.raws.Add(1)
			return func() { f.restores.Add(1) }, nil
		},
		size:    func() pssh.WindowSize { return pssh.WindowSize{Width: 80, Height: 24} },
		changes: func(<-chan struct{}) <-chan pssh.WindowSize { return nil },
	}
	return f
}

func TestAttachmentDetach(t *testing.T) {
	c := &fakeClient{results: []func(context.Context) error{result(nil)}}
	f := newAttachFixture(c)
	if err := f.a.run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c.calls != 1 {
		t.Errorf("got %d attaches, want 1", c.calls)
	}
	if f.raws.Load() != 1 || f.restores.Load() != 1 {
		t.Errorf("raw mode set %d times and restored %d, want 1 and 1", f.raws.Load(), f.restores.Load())
	}
	if f.a.attached.Load() {
		t.Error("still marked as attached")
	}
}

func TestAttachmentReattaches(t *testing.T) {
	c := &fakeClient{results: []func(context.Context) error{
		result(&ssh.ExitMissingError{}),
		result(fmt.Errorf("lost: %w", io.EOF)),
		result(nil),
	}}
	f := newAttachFixture(c)
	if err := f.a.run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c.calls != 3 || c.waits != 3 {
		t.Errorf("got %d attaches and %d waits, want 3 and 3", c.calls, c.waits)
	}
	if f.raws.Load() != 3 || f.restores.Load() != 3 {
		t.Errorf("raw mode set %d times and restored %d, want 3 and 3", f.raws.Load(), f.restores.Load())
	}
	lost := 0
	for _, s := range f.statuses {
		if strings.Contains(s, "lost") && strings.Contains(s, "reconnecting") {
			lost++
		}
	}
	if lost != 2 {
		t.Errorf("got %d reconnection messages in %q, want 2", lost, f.statuses)
	}
}

func TestAttachmentCommandFailure(t *testing.T) {
	// tmux, or the shell, failing is not something that reconnecting fixes.
	failure := &ssh.ExitError{Waitmsg: ssh.Waitmsg{}}
	c := &fakeClient{results: []func(context.Context) error{result(failure)}}
	f := newAttachFixture(c)
	if err := f.a.run(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("got %v, want %v", err, failure)
	}
	if c.calls != 1 {
		t.Errorf("got %d attaches, want 1", c.calls)
	}
}

func TestAttachmentCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	c := &fakeClient{results: []func(context.Context) error{
		func(ctx context.Context) error {
			cancel()
			<-ctx.Done()
			return ctx.Err()
		},
	}}
	f := newAttachFixture(c)
	if err := f.a.run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want %v", err, context.Canceled)
	}
	if f.restores.Load() != 1 {
		t.Errorf("the terminal was restored %d times, want 1", f.restores.Load())
	}
}

func TestAttachmentCancelledWhilstWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	// The connection is never established.
	c := &fakeClient{connected: make(chan struct{})}
	f := newAttachFixture(c)
	done := make(chan error, 1)
	go func() { done <- f.a.run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want %v", err, context.Canceled)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out")
	}
	if c.calls != 0 || f.raws.Load() != 0 {
		t.Errorf("attached %d times, set raw mode %d, want neither", c.calls, f.raws.Load())
	}
}

func TestAttachmentClientClosed(t *testing.T) {
	c := &fakeClient{results: []func(context.Context) error{result(pssh.ErrClientClosed)}}
	f := newAttachFixture(c)
	if err := f.a.run(t.Context()); !errors.Is(err, pssh.ErrClientClosed) {
		t.Fatalf("got %v, want %v", err, pssh.ErrClientClosed)
	}
	if c.calls != 1 {
		t.Errorf("got %d attaches, want 1", c.calls)
	}
}

func TestAttachmentRawModeFailure(t *testing.T) {
	c := &fakeClient{}
	f := newAttachFixture(c)
	f.a.raw = func() (func(), error) { return nil, errors.New("not a terminal") }
	if err := f.a.run(t.Context()); err == nil || !strings.Contains(err.Error(), "not a terminal") {
		t.Fatalf("got %v, want the raw mode error", err)
	}
	if c.calls != 0 {
		t.Errorf("attached %d times, want 0", c.calls)
	}
}

func TestAttachmentWaitsForConnection(t *testing.T) {
	c := &fakeClient{connected: make(chan struct{}), results: []func(context.Context) error{result(nil)}}
	f := newAttachFixture(c)
	done := make(chan error, 1)
	go func() { done <- f.a.run(t.Context()) }()
	select {
	case err := <-done:
		t.Fatalf("attached before there was a connection: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(c.connected)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out")
	}
}

const tmuxTestConfig = `
connections:
  - name: dev
    host: 127.0.0.1:2222
    user: auser
`

func dispatchTmux(t *testing.T, cfg string, args ...string) error {
	t.Helper()
	cfgPath := writeTestConfig(t, t.TempDir(), cfg)
	cli := newTestCLI(t)
	return cli.DispatchWithArgs(t.Context(), "pconn", append([]string{"--config=" + cfgPath, "tmux"}, args...)...)
}

func TestTmuxCommandErrors(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"nosuch"}, `connection "nosuch" not found (available: dev)`},
		// Tests do not run on a terminal.
		{[]string{"dev"}, "requires standard input to be a terminal"},
	} {
		err := dispatchTmux(t, tmuxTestConfig, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: got %v, want an error containing %q", tc.args, err, tc.want)
		}
	}
	if err := dispatchTmux(t, tmuxTestConfig); err == nil {
		t.Error("expected an error without a connection")
	}
}

// TestTmuxLogsToFile checks that logs do not go to the terminal, where they
// would corrupt control mode, unless a destination was configured.
func TestTmuxLogsToFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	tmuxLogPath = ""
	_ = dispatchTmux(t, tmuxTestConfig, "dev")
	if !strings.HasPrefix(tmuxLogPath, tmp) {
		t.Errorf("got log path %q, want a file in %v", tmuxLogPath, tmp)
	}
	if _, err := os.Stat(tmuxLogPath); err != nil {
		t.Error(err)
	}

	explicit := filepath.Join(t.TempDir(), "explicit.log")
	tmuxLogPath = ""
	_ = dispatchTmux(t, "logging:\n  file: "+explicit+"\n"+tmuxTestConfig, "dev")
	if tmuxLogPath != explicit {
		t.Errorf("got log path %q, want the configured %q", tmuxLogPath, explicit)
	}
}
