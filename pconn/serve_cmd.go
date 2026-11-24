// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloudeng.io/cmdutil/cmdconfig"
	"cloudeng.io/logging/ctxlog"
)

// ServeCommand implements the 'serve' subcommand, which serves a page listing
// the configured connections and starts and stops them as the page asks.
type ServeCommand struct{}

// ServeFlags defines flags for the serve subcommand.
type ServeFlags struct {
	Config cmdconfig.FilesFlag `subcmd:"config,,YAML configuration file (overrides global --config)"`
	//nolint:lll // The usage has to say what the address means.
	HTTP             string        `subcmd:"http,localhost:8099,'address to serve the page on; a bare port is served on the loopback interface, eg. 8099, and an address names the interface to serve on, eg. 0.0.0.0:8099'"`
	UnlimitedRetries bool          `subcmd:"unlimited-retries,true,continue retrying connections indefinitely"`
	StatusInterval   time.Duration `subcmd:"status-interval,15s,how often to report on connections that have not yet been established; 0 disables"`
	//nolint:lll // The usage has to say when this is worth using.
	InterfaceRecovery bool `subcmd:"interface-recovery,false,'retry a connection that cannot be routed to by way of each local interface in turn; only of use when this host is attached to the target subnet more than once, such as over both ethernet and wi-fi'"`
}

func (sf *ServeFlags) connOpts() connOpts {
	return connOpts{
		unlimitedRetries:  sf.UnlimitedRetries,
		interfaceRecovery: sf.InterfaceRecovery,
	}
}

// Run executes the serve command. It serves the page until the context is
// cancelled and then stops every connection it started, which is what releases
// their local ports.
func (c ServeCommand) Run(ctx context.Context, flags any, args []string) error {
	sf := flags.(*ServeFlags)

	cfg, err := configForCommand(ctx, &sf.Config)
	if err != nil {
		return err
	}
	if strings.TrimSpace(sf.HTTP) == "" {
		return fmt.Errorf("--http must name the address to serve the page on, eg. 8099 or localhost:8099")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	logger := ctxlog.Logger(ctx)
	reporter := newStatusReporter(logger, sf.StatusInterval)
	sup := newSupervisor(runCtx, cfg, sf.connOpts(), logger, reporter)

	// Bound before anything is started, so that an address which cannot be
	// served is reported rather than leaving connections up with no way to
	// reach the page that lists them.
	web, err := listenWeb(sf.HTTP, sup, logger)
	if err != nil {
		return err
	}

	// Whatever was named is brought up at once; the rest wait to be started
	// from the page.
	if len(args) > 0 {
		if err := sup.Start(args...); err != nil {
			return err
		}
	}

	go reporter.run(runCtx)

	logger.Info("serving the list of connections",
		"url", web.url(),
		"started", startedDesc(args),
	)
	// Returns when the context is done, which is what ends serving.
	web.serve(runCtx)

	// The connections outlive the server by as long as it takes them to stop,
	// which is waited for so that the ports are released before pconn exits.
	cancel()
	sup.StopAll()
	logger.Info("stopped serving the list of connections", "url", web.url())
	return nil
}

// startedDesc describes what was brought up at startup, for the log line that
// says the page is being served.
func startedDesc(args []string) string {
	if len(args) == 0 {
		return "nothing yet"
	}
	return strings.Join(args, ", ")
}
