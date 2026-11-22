// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/subcmd"
	"cloudeng.io/net/pssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// TmuxCommand implements the 'tmux' subcommand, which attaches to a tmux
// session over a persistent connection.
type TmuxCommand struct{}

// TmuxFlags defines flags for the tmux subcommand.
type TmuxFlags struct {
	Command   string        `subcmd:"command,,'the complete command line to run on the remote host, eg. \"/opt/homebrew/bin/tmux -CC new -A -s main\"; overrides tmux.command in the connection configuration'"`
	KeepAlive time.Duration `subcmd:"keep-alive,15s,'how often to probe the connection so that one that has silently failed is noticed and replaced; 0 disables'"`
}

// tmuxLogPattern names the log file created when none is configured, since
// logging to the terminal would corrupt a tmux control mode session.
const tmuxLogPattern = "pconn-tmux-XXXXXX.log"

// tmuxLogPath is the file that the tmux command's logger writes to, set by
// tmuxLoggerPrehook.
var tmuxLogPath string

var tmuxLoggerPrehook = subcmd.LoggerHook("tmuxLoggerPrehook", func(ctx context.Context, fs *flag.FlagSet) (*cmdutil.Logger, error) {
	cfg, _ := cmdutil.ContextValue[Config](ctx)
	lc := cfg.Logging.WithFlagOverrides(fs, globalFlags.LoggingFlags)
	if lc.File == "" && lc.TempFile == "" {
		lc.TempFile = tmuxLogPattern
	}
	logger, err := lc.NewLogger()
	if err != nil {
		return nil, err
	}
	tmuxLogPath = logger.Path()
	return logger, nil
})

var tmuxPrehook = configPrehookWith(tmuxLoggerPrehook)

// tmuxCommandLine returns the command line to run: that of the flag, else of
// the connection.
func tmuxCommandLine(tf *TmuxFlags, conn ConnectionConfig) string {
	if tf.Command != "" {
		return tf.Command
	}
	return conn.Tmux.Command
}

// Run executes the tmux command.
func (c TmuxCommand) Run(ctx context.Context, flags any, args []string) error {
	tf := flags.(*TmuxFlags)
	if len(args) != 1 {
		return fmt.Errorf("exactly one connection must be named, got %d", len(args))
	}
	cfg, ok := cmdutil.ContextValue[Config](ctx)
	if !ok {
		return fmt.Errorf("no configuration found in context")
	}
	conn, err := cfg.Connection(args[0])
	if err != nil {
		return err
	}
	cmdLine := tmuxCommandLine(tf, conn)
	if strings.TrimSpace(cmdLine) == "" {
		return fmt.Errorf("connection %q: tmux.command or --command must specify the command line to run, eg. /opt/homebrew/bin/tmux -CC new -A -s main", conn.Name)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) { //nolint:gosec // fds fit in an int.
		return fmt.Errorf("tmux requires standard input to be a terminal")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var attached atomic.Bool
	status := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "pconn: "+format+"\n", args...)
	}
	if tmuxLogPath != "" {
		status("logging to %s", tmuxLogPath)
	}
	psshOpts := append(identityOptions(conn),
		pssh.WithDialTimeout(conn.DialTimeout),
		pssh.WithKeepAlive(tf.KeepAlive),
		// Only say why a connection cannot be made when nothing is attached,
		// since anything written to the terminal then would corrupt the
		// session.
		pssh.WithOnAttemptFailed(func(err error, _ bool) {
			if !attached.Load() {
				status("connecting to %s failed: %v", conn.Name, err)
			}
		}),
	)
	client := pssh.NewClient(runCtx, "tcp", conn.HostPort(), psshOpts...)
	controller := newBackoffController(true)
	defer controller.Stop()

	// The connection is maintained for as long as the attachment runs, and a
	// failure to connect that retrying will not fix ends it.
	var connErr error
	connDone := make(chan struct{})
	go func() {
		defer close(connDone)
		err := client.ConnectAndWait(runCtx, controller.Backoff)
		if err != nil && !isCleanShutdown(err) && !errors.Is(err, pssh.ErrClientClosed) {
			connErr = fmt.Errorf("connection %q (%s): %w", conn.Name, conn.HostPort(), err)
			cancel()
		}
	}()

	a := &attachment{
		client:     client,
		name:       conn.Name,
		cmd:        cmdLine,
		status:     status,
		attached:   &attached,
		in:         os.Stdin,
		out:        os.Stdout,
		retryDelay: time.Second,
	}
	a.useLocalTerminal()
	err = a.run(runCtx)
	cancel()
	client.Close()
	<-connDone
	switch {
	case connErr != nil:
		return connErr
	case ctx.Err() != nil:
		// Shutting down because the caller asked is not a failure.
		return nil
	}
	return err
}

// attacher is the part of a pssh.Client that an attachment uses.
type attacher interface {
	WaitConnected(ctx context.Context) error
	RunInteractive(ctx context.Context, cmd string, opts ...pssh.RunOption) error
}

// attachment runs a command on a terminal over a connection, rerunning it if
// the connection is lost.
type attachment struct {
	client     attacher
	name       string
	cmd        string
	status     func(format string, args ...any)
	attached   *atomic.Bool
	in         io.Reader
	out        io.Writer
	termType   string
	raw        func() (restore func(), err error)
	size       func() pssh.WindowSize
	changes    func(stop <-chan struct{}) <-chan pssh.WindowSize
	retryDelay time.Duration
}

// useLocalTerminal attaches the process's terminal.
func (a *attachment) useLocalTerminal() {
	fd := int(os.Stdin.Fd()) //nolint:gosec
	a.termType = os.Getenv("TERM")
	a.raw = func() (func(), error) {
		state, err := term.MakeRaw(fd)
		if err != nil {
			return nil, fmt.Errorf("setting the terminal to raw mode: %w", err)
		}
		return func() { _ = term.Restore(fd, state) }, nil
	}
	a.size = func() pssh.WindowSize {
		for _, f := range []*os.File{os.Stdout, os.Stdin} {
			if w, h, err := term.GetSize(int(f.Fd())); err == nil { //nolint:gosec
				return pssh.WindowSize{Width: w, Height: h}
			}
		}
		return pssh.WindowSize{}
	}
	a.changes = func(stop <-chan struct{}) <-chan pssh.WindowSize {
		return watchWindowSize(stop, a.size)
	}
}

// connectionLost reports whether err, from RunInteractive, means that the
// connection failed as opposed to the command having run and failed.
func connectionLost(err error) bool {
	var missing *ssh.ExitMissingError
	var exit *ssh.ExitError
	switch {
	case errors.As(err, &missing), errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		return true
	case errors.As(err, &exit):
		// The command was killed, as it is when the server hangs up.
		return exit.Signal() != ""
	}
	return false
}

// run attaches until the command exits, ctx is done or an error that
// reconnecting will not fix occurs. If the connection is lost it waits for the
// connection to be replaced and attaches again.
func (a *attachment) run(ctx context.Context) error {
	a.status("connecting to %s to run %s", a.name, a.cmd)
	for {
		if err := a.client.WaitConnected(ctx); err != nil {
			return err
		}
		err := a.attach(ctx)
		switch {
		case err == nil:
			// The user detached.
			return nil
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, pssh.ErrClientClosed), !connectionLost(err):
			return commandFailure(err)
		}
		a.status("connection to %s lost (%v), reconnecting", a.name, err)
		select {
		case <-time.After(a.retryDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// attach runs the command once, with the terminal in raw mode.
func (a *attachment) attach(ctx context.Context) error {
	restore, err := a.raw()
	if err != nil {
		return err
	}
	defer restore()
	stop := make(chan struct{})
	defer close(stop)
	a.attached.Store(true)
	defer a.attached.Store(false)
	err = a.client.RunInteractive(ctx, a.cmd,
		pssh.WithLoginShell(),
		pssh.WithStdin(a.in),
		pssh.WithStdout(a.out),
		pssh.WithTerminal(a.termType, a.size()),
		pssh.WithWindowChanges(a.changes(stop)),
	)
	return err
}

// commandFailure explains the exit status that a shell uses for a command that
// it cannot find, which is the likely cause of it when starting tmux.
func commandFailure(err error) error {
	var exit interface{ ExitStatus() int } // An *ssh.ExitError.
	if errors.As(err, &exit) && exit.ExitStatus() == 127 {
		return fmt.Errorf("%w: tmux was not found using the login shell's PATH, which does not include "+
			"that set in interactive-only files such as .zshrc; give its full path in tmux.command in the "+
			"connection's configuration or the --command flag", err)
	}
	return err
}
