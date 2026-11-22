// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

// pconn manages a set of persistent network connections (such as SSH) and port forwards.
//
// It establishes and maintains persistent connections based on a YAML configuration
// file, automatically reconnecting dropped connections using exponential backoff.
// Connections can belong to multiple groups, allowing sets of related connections
// to be started and maintained together.
//
// The tmux command attaches to a tmux session on one of the connections,
// running the tmux command that the connection's tmux.args configures, eg.
// 'new -A -s main' to create or attach to a session, with tmux.options such as
// -CC for the control mode supported by iTerm2. If the connection is lost it is reestablished and the
// session reattached, which leaves the session itself, and what is running in
// it, undisturbed. Logs are written to a temporary file unless a log file is
// configured, since anything else written to the terminal would corrupt the
// session.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"syscall"

	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/cmdconfig"
	"cloudeng.io/cmdutil/subcmd"
	"cloudeng.io/net/pssh"
)

const CmdSpecYAML = `
name: pconn
summary: manage persistent network connections
commands:
  - name: connect
    summary: create and maintain persistent connections for one or more groups or all configured connections
    args:
      - "<group>... - optional group(s) of connections to connect (use 'all' for all connections); defaults to all if omitted"
  - name: tmux
    summary: attach to a tmux session on a connection, reconnecting and reattaching if it is lost
    args:
      - "<connection> - name of the configured connection to attach over"
`

type GlobalFlags struct {
	Config cmdconfig.FilesFlag `subcmd:"config,config.yaml,'YAML configuration file; if unset, config.yaml in the pconn directory of the user configuration directories is used. The current directory is not searched unless a file is named here'" doc:"YAML configuration file"`
	cmdutil.LoggingFlags
	Verbose bool `subcmd:"verbose,false,enable verbose logging" doc:"enable verbose logging"`
}

var globalFlags GlobalFlags

func createCLI() *subcmd.CommandSetYAML {
	cmdSet := subcmd.MustFromYAML(CmdSpecYAML)

	connectCmd := ConnectCommand{}
	cmdSet.Set("connect").MustRunner(connectCmd.Run, &ConnectFlags{})
	cmdSet.Set("connect").MustSetPreHooks(configPrehook)

	tmuxCmd := TmuxCommand{}
	cmdSet.Set("tmux").MustRunner(tmuxCmd.Run, &TmuxFlags{})
	cmdSet.Set("tmux").MustSetPreHooks(tmuxPrehook)

	return cmdSet
}

// appDir is pconn's own directory within each of the user's configuration
// directories, see cmdconfig.SearchPath.
const appDir = "pconn"

// configSpec specifies how the configuration file named by the global --config
// is located, see configSpecFor.
func configSpec() cmdconfig.Spec {
	return configSpecFor(&globalFlags.Config)
}

// configSpecFor specifies how the files held by a --config flag are located:
// in pconn's own configuration directories, and one must be there. Each flag
// has its own spec because whether the current directory is searched depends
// on whether that flag names a file, see searchPath; connect has a --config of
// its own that overrides the global one.
func configSpecFor(files *cmdconfig.FilesFlag) cmdconfig.Spec {
	return cmdconfig.Spec{
		Path:      searchPath(files.HasSet(cmdconfig.FileTypeYAML)),
		FirstOnly: []cmdconfig.FileType{cmdconfig.FileTypeYAML},
	}
}

// searchPath returns the directories that the configuration file is looked for
// in: pconn's own, and the current directory as well only when a file was
// named on the command line.
//
// The current directory is not searched by default because pconn is run from
// wherever the connections it maintains are wanted, and a config.yaml there
// belongs to whatever is being worked on rather than to pconn. A file in the
// current directory is read when it is asked for, since --config resolves a
// relative name against the full search path as any other command would.
func searchPath(explicit bool) string {
	if explicit {
		return cmdconfig.SearchPath(appDir)
	}
	return cmdconfig.SearchPathUserDirs(appDir)
}

// loggerPrehook creates the logger from the configuration, as overridden by the
// command line flags.
var loggerPrehook = subcmd.LoggerHook("withLoggerPrehook", func(ctx context.Context, fs *flag.FlagSet) (*cmdutil.Logger, error) {
	cfg, _ := cmdutil.ContextValue[Config](ctx)
	return cfg.Logging.WithFlagOverrides(fs, globalFlags.LoggingFlags).NewLogger()
})

// configPrehookWith returns a hook that loads the configuration and then runs
// the hook that creates the logger, which is that of the command to be run.
func configPrehookWith(loggerHook subcmd.PreHook) subcmd.PreHook {
	return func(ctx context.Context) (context.Context, string, subcmd.PostHook, error) {
		hook := cmdconfig.ConfigHook[Config]{
			Files:    &globalFlags.Config,
			Spec:     configSpec(),
			Required: true,
		}
		ctx, id, _, err := hook.Run(ctx)
		if err != nil {
			return ctx, id, nil, err
		}
		ctx, _, postHook, err := loggerHook(ctx)
		return ctx, id, postHook, err
	}
}

var configPrehook = configPrehookWith(loggerPrehook)

// isCleanShutdown reports whether err is a clean shutdown for this command:
// a cancellation or interrupt, as per cmdutil.IsCleanShutdown, or the pssh
// client having been closed. Every error must be clean, since maintainConnections
// aggregates the errors from all of its connections and a run that was both
// interrupted and genuinely broken must still report the failure.
func isCleanShutdown(err error) bool {
	return cmdutil.IsCleanShutdown(err, false, pssh.ErrClientClosed)
}

func main() {
	ctx := context.Background()
	ctx, cancel := context.WithCancelCause(ctx)
	cli := createCLI()
	fs := subcmd.NewFlagSet().MustRegisterFlagStruct(&globalFlags, nil, nil)
	cli.WithGlobalFlags(fs)

	cmdutil.HandleSignals(func() { cancel(cmdutil.ErrInterrupt) }, os.Interrupt, syscall.SIGTERM)
	if err := cli.Dispatch(ctx); err != nil {
		if isCleanShutdown(err) {
			return
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
