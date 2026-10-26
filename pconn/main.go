// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

// pconn manages a set of persistent network connections (such as SSH) and port forwards.
//
// It establishes and maintains persistent connections based on a YAML configuration
// file, automatically reconnecting dropped connections using exponential backoff.
// Connections can belong to multiple groups, allowing sets of related connections
// to be started and maintained together.
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
`

type GlobalFlags struct {
	Config cmdconfig.FilesFlag `subcmd:"config,config.yaml,YAML configuration file" doc:"YAML configuration file"`
	cmdutil.LoggingFlags
	Verbose bool `subcmd:"verbose,false,enable verbose logging" doc:"enable verbose logging"`
}

var globalFlags GlobalFlags

func createCLI() *subcmd.CommandSetYAML {
	cmdSet := subcmd.MustFromYAML(CmdSpecYAML)

	connectCmd := ConnectCommand{}
	cmdSet.Set("connect").MustRunner(connectCmd.Run, &ConnectFlags{})
	cmdSet.Set("connect").MustSetPreHooks(configPrehook)

	return cmdSet
}

// configSpec specifies how the configuration file is located: in the standard
// locations for pconn, and it must be there.
func configSpec() cmdconfig.Spec {
	return cmdconfig.Spec{
		Path:      cmdconfig.SearchPath("pconn"),
		FirstOnly: []cmdconfig.FileType{cmdconfig.FileTypeYAML},
	}
}

// loggerPrehook creates the logger from the configuration, as overridden by the
// command line flags.
var loggerPrehook = subcmd.LoggerHook("withLoggerPrehook", func(ctx context.Context, fs *flag.FlagSet) (*cmdutil.Logger, error) {
	cfg, _ := cmdutil.ContextValue[Config](ctx)
	return cfg.Logging.WithFlagOverrides(fs, globalFlags.LoggingFlags).NewLogger()
})

func configPrehook(ctx context.Context) (context.Context, string, subcmd.PostHook, error) {
	hook := cmdconfig.ConfigHook[Config]{
		Files:    &globalFlags.Config,
		Spec:     configSpec(),
		Required: true,
	}
	ctx, id, _, err := hook.Run(ctx)
	if err != nil {
		return ctx, id, nil, err
	}
	ctx, _, postHook, err := loggerPrehook(ctx)
	return ctx, id, postHook, err
}

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
