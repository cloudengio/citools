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
	"fmt"
	"os"
	"syscall"

	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/cmdyaml"
	"cloudeng.io/cmdutil/subcmd"
	"cloudeng.io/logging/ctxlog"
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
	ConfigFile string `subcmd:"config,pconn.yaml,path to YAML configuration file" doc:"path to YAML configuration file"`
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

func configPrehook(ctx context.Context) (context.Context, string, subcmd.PostHook, error) {
	id := "withConfigPrehook"
	postHook := func(ctx context.Context) (string, error) { return id, nil }
	fs := subcmd.GlobalFlagSetFromContext(ctx)
	if fs == nil {
		return ctx, id, postHook, fmt.Errorf("global flagset not in context")
	}

	configFile := globalFlags.ConfigFile
	if fl := fs.FlagSet().Lookup("config"); fl != nil && fl.Value.String() != "" {
		configFile = fl.Value.String()
	} else if configFile == "pconn.yaml" {
		if _, err := os.Stat("pconn.yaml"); os.IsNotExist(err) {
			if _, err := os.Stat("sshpcon.yaml"); err == nil {
				configFile = "sshpcon.yaml"
			}
		}
	}

	var cfg Config
	if err := cmdyaml.ParseConfigFilesStrict(ctx, &cfg, configFile); err != nil {
		return ctx, id, postHook, fmt.Errorf("failed to read configuration file %q: %w\nedit that file to correct it", configFile, err)
	}
	if err := cfg.Validate(); err != nil {
		return ctx, id, postHook, fmt.Errorf("configuration file %q is invalid: %w\nedit that file to correct it", configFile, err)
	}

	loggerConfig := cfg.Logging.WithFlagOverrides(
		fs.FlagSet(), globalFlags.LoggingFlags)
	logger, err := loggerConfig.NewLogger()
	if err != nil {
		return ctx, id, postHook, fmt.Errorf("error setting up logger: %w", err)
	}

	ctx = ctxlog.WithLogger(ctx, logger.Logger)
	ctx = ContextWithConfig(ctx, cfg)
	postHook = func(ctx context.Context) (string, error) {
		if err := logger.Close(); err != nil {
			return id, fmt.Errorf("error closing logger: %w", err)
		}
		return id, nil
	}
	return ctx, id, postHook, nil
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
