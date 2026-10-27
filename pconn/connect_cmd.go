// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"cloudeng.io/algo/ratecontrol"
	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/cmdconfig"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/net/pssh"
	"cloudeng.io/sync/errgroup"
	"golang.org/x/crypto/ssh"
)

// ConnectCommand implements the 'connect' subcommand to create and maintain
// persistent connections.
type ConnectCommand struct{}

// ConnectFlags defines flags for the connect subcommand.
type ConnectFlags struct {
	Config           cmdconfig.FilesFlag `subcmd:"config,,YAML configuration file (overrides global --config)"`
	Group            string              `subcmd:"group,,comma-separated list of groups of connections to connect"`
	UnlimitedRetries bool                `subcmd:"unlimited-retries,true,continue retrying connections indefinitely"`
	FailFast         bool                `subcmd:"fail-fast,false,exit immediately if any connection fails permanently"`
	StatusInterval   time.Duration       `subcmd:"status-interval,15s,how often to report on connections that have not yet been established; 0 disables"`
	//nolint:lll // The usage has to say when this is worth using.
	InterfaceRecovery bool `subcmd:"interface-recovery,false,'retry a connection that cannot be routed to by way of each local interface in turn; only of use when this host is attached to the target subnet more than once, such as over both ethernet and wi-fi'"`
}

func newBackoffController(unlimitedRetries bool) *ratecontrol.Controller {
	return ratecontrol.New(
		ratecontrol.WithBackoff(func() ratecontrol.Backoff {
			return ratecontrol.NewExponentialBackoff(
				500*time.Millisecond,
				4,
				ratecontrol.WithUnlimitedRetries(unlimitedRetries),
			)
		}),
	)
}

// configFileNames names the configuration files for an error message. What
// was located is preferred, since it is what was actually read, and the flag
// is the fallback for when nothing was: an error naming no file at all says
// less than one naming what was asked for.
func configFileNames(loc *cmdconfig.Located, files *cmdconfig.FilesFlag) string {
	if loc != nil && len(loc.YAML) > 0 {
		return strings.Join(loc.YAML, ", ")
	}
	if files != nil {
		if named := files.Files(cmdconfig.FileTypeYAML, false); len(named) > 0 {
			return strings.Join(named, ", ")
		}
	}
	return "(none)"
}

// Run executes the connect command.
func (c ConnectCommand) Run(ctx context.Context, flags any, args []string) error {
	cf := flags.(*ConnectFlags)

	cfg, ok := cmdutil.ContextValue[Config](ctx)
	if !ok {
		return fmt.Errorf("no configuration found in context")
	}

	if cf.Config.HasSet(cmdconfig.FileTypeYAML) {
		var newCfg Config
		loc, err := cmdconfig.Load(ctx, &newCfg, &cf.Config, configSpecFor(&cf.Config))
		if err != nil {
			return fmt.Errorf("failed to read the configuration file: %w", err)
		}
		if err := newCfg.Validate(); err != nil {
			return fmt.Errorf("configuration file %q is invalid: %w", configFileNames(loc, &cf.Config), err)
		}
		cfg = newCfg
	}

	var targets []string
	if cf.Group != "" {
		for _, g := range strings.Split(cf.Group, ",") {
			if g = strings.TrimSpace(g); g != "" {
				targets = append(targets, g)
			}
		}
	}
	targets = append(targets, args...)

	var connsToMaintain []ConnectionConfig
	var targetDesc string

	if len(targets) > 0 {
		seen := make(map[string]bool)
		hasAll := false
		for _, target := range targets {
			if target == "all" {
				hasAll = true
			}
			matched, err := cfg.ConnectionsForGroup(target)
			if err != nil {
				var byName []ConnectionConfig
				for _, conn := range cfg.Connections {
					if conn.Name == target {
						byName = append(byName, conn)
					}
				}
				if len(byName) == 0 {
					return err
				}
				matched = byName
			}
			for _, conn := range matched {
				if !seen[conn.Name] {
					seen[conn.Name] = true
					connsToMaintain = append(connsToMaintain, conn)
				}
			}
		}
		if port, c1, c2, conflict := HasConflictingPorts(connsToMaintain); conflict {
			if hasAll {
				groups := cfg.Groups()
				return fmt.Errorf("multiple groups configured with conflicting port %d (between %q and %q); please specify non-conflicting groups to connect (available: %s)",
					port, c1, c2, strings.Join(groups, ", "))
			}
			return fmt.Errorf("conflict on local port %d between connection %q and %q", port, c1, c2)
		}
		if len(targets) == 1 && targets[0] == "all" {
			targetDesc = fmt.Sprintf("all connections (%d connections)", len(connsToMaintain))
		} else if len(targets) == 1 {
			targetDesc = fmt.Sprintf("group %q (%d connections)", targets[0], len(connsToMaintain))
		} else {
			targetDesc = fmt.Sprintf("groups [%s] (%d connections)", strings.Join(targets, ", "), len(connsToMaintain))
		}
	} else {
		groups := cfg.Groups()
		if len(groups) == 1 && len(cfg.ConnectionsWithoutGroup()) == 0 {
			matched, _ := cfg.ConnectionsForGroup(groups[0])
			connsToMaintain = matched
			targetDesc = fmt.Sprintf("group %q (%d connections)", groups[0], len(connsToMaintain))
		} else if len(groups) > 1 {
			if port, c1, c2, conflict := cfg.HasConflictingPortsAcrossAll(); conflict {
				return fmt.Errorf("multiple groups configured with conflicting port %d (between %q and %q); please specify a group to connect (available: %s)",
					port, c1, c2, strings.Join(groups, ", "))
			}
			connsToMaintain = cfg.Connections
			targetDesc = fmt.Sprintf("all connections (%d connections)", len(connsToMaintain))
		} else {
			connsToMaintain = cfg.Connections
			targetDesc = fmt.Sprintf("all connections (%d connections)", len(connsToMaintain))
		}
	}

	if len(connsToMaintain) == 0 {
		return fmt.Errorf("no connections configured to maintain")
	}

	logger := ctxlog.Logger(ctx)
	logger.Info("starting persistent connection management", "target", targetDesc)
	return maintainConnections(ctx, logger, connsToMaintain, cf)
}

func filterConnections(conns []ConnectionConfig, names []string) ([]ConnectionConfig, error) {
	if len(names) == 0 {
		return conns, nil
	}
	nameMap := make(map[string]ConnectionConfig)
	for _, c := range conns {
		nameMap[c.Name] = c
	}

	var selected []ConnectionConfig
	var missing []string
	for _, name := range names {
		if c, ok := nameMap[name]; ok {
			selected = append(selected, c)
		} else {
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		var available []string
		for _, c := range conns {
			available = append(available, c.Name)
		}
		return nil, fmt.Errorf("connection(s) not found: %s (available: %s)",
			strings.Join(missing, ", "), strings.Join(available, ", "))
	}

	return selected, nil
}

type connRunner struct {
	conn       ConnectionConfig
	client     *pssh.Client
	controller *ratecontrol.Controller
	waiter     *connWaiter
}

func maintainConnections(
	ctx context.Context,
	logger *slog.Logger,
	conns []ConnectionConfig,
	cf *ConnectFlags,
) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	reporter := newStatusReporter(logger, cf.StatusInterval)
	runners := make([]*connRunner, len(conns))
	for i, conn := range conns {
		switch conn.Type {
		case "ssh":
			hostPort := conn.HostPort()
			connLogger := logger.With("connection", conn.Name, "type", conn.Type)
			unlimited := conn.ShouldUnlimitedRetries(cf.UnlimitedRetries)
			// pssh adds ssh.network and ssh.addr to the logger it is given,
			// so only the waiter's own lines need the address adding.
			waiter := reporter.add(connLogger.With("ssh.addr", hostPort), conn, unlimited)

			var psshOpts []pssh.Option
			psshOpts = append(psshOpts,
				pssh.WithDialTimeout(conn.DialTimeout),
				pssh.WithInterfaceRecovery(cf.InterfaceRecovery),
				pssh.WithLogger(connLogger),
				pssh.WithOnConnect(func(c *pssh.Client) {
					key, comment := c.AuthKey()
					keyName := comment
					if keyName == "" && key != nil {
						keyName = ssh.FingerprintSHA256(key)
					}
					// The waiter logs this, so that a connection which was
					// reported as pending is reported as established at the
					// same level, and a reconnection as a reconnection.
					waiter.established(keyName)
				}),
				// Without this the reason a connection cannot be made is
				// never reported: pssh retries, so it does not return it.
				pssh.WithOnAttemptFailed(waiter.attemptFailed),
			)
			if conn.parsedUser != "" {
				psshOpts = append(psshOpts, pssh.WithUser(conn.parsedUser))
			}
			if conn.AcceptNewHostKeys {
				psshOpts = append(psshOpts, pssh.WithAcceptNewHostKeys(true))
			}
			if len(conn.KnownHosts) > 0 {
				psshOpts = append(psshOpts, pssh.WithKnownHosts(conn.KnownHosts...))
			}
			for _, f := range conn.Forwards {
				psshOpts = append(psshOpts, pssh.WithLocalPortForward(f.LocalPort, f.RemotePort))
			}

			client := pssh.NewClient(runCtx, "tcp", hostPort, psshOpts...)
			controller := newBackoffController(unlimited)

			runners[i] = &connRunner{
				conn:       conn,
				client:     client,
				controller: controller,
				waiter:     waiter,
			}
		default:
			return fmt.Errorf("connection %q: unsupported connection type %q", conn.Name, conn.Type)
		}
	}

	var g *errgroup.T
	if cf.FailFast {
		// WithCancel cancels runCtx as soon as one connection fails, which is
		// what --fail-fast asks for.
		g = errgroup.WithCancel(cancel)
	} else {
		// A plain T lets every connection run to completion and collects all
		// of their errors.
		g = &errgroup.T{}
	}

	// Whilst a connection is being retried the reporter is the only thing
	// that says anything at all, so it runs for as long as the connections
	// do. runCtx is always cancelled by the deferred cancel above, so this
	// goroutine never outlives the call.
	go reporter.run(runCtx)

	for _, r := range runners {
		g.Go(func() error {
			defer r.controller.Stop()

			hostPort := r.conn.HostPort()
			logger.Info("maintaining persistent connection",
				"connection", r.conn.Name,
				"type", r.conn.Type,
				"host", hostPort,
				"user", r.conn.ParsedUser(),
				"dial_timeout", r.conn.DialTimeout,
				"unlimited_retries", r.conn.ShouldUnlimitedRetries(cf.UnlimitedRetries),
				"interface_recovery", cf.InterfaceRecovery,
				"forwards", formatForwards(r.conn.Forwards),
			)

			r.waiter.connecting()
			err := r.client.ConnectAndWait(runCtx, r.controller.Backoff)
			// The errors.Is accepts ErrClientClosed anywhere in err, whereas
			// isCleanShutdown requires every error to be clean.
			if err != nil && !isCleanShutdown(err) && !errors.Is(err, pssh.ErrClientClosed) {
				logger.Error("connection terminated with error",
					"connection", r.conn.Name,
					"type", r.conn.Type,
					"host", hostPort,
					"err", err,
				)
				return fmt.Errorf("connection %q (%s): %w", r.conn.Name, hostPort, err)
			}
			logger.Info("connection closed",
				"connection", r.conn.Name,
				"type", r.conn.Type,
				"host", hostPort,
			)
			return nil
		})
	}

	// Closing the clients is what releases ConnectAndWait, so whichever
	// cancellation arrives first -- the caller's, or the one WithCancel issues
	// on the first failure -- has to reach them. The deferred cancel above
	// always cancels runCtx, so this goroutine never outlives the call.
	go func() {
		<-runCtx.Done()
		for _, r := range runners {
			r.client.Close()
		}
	}()

	// Wait returns an errors.M holding whatever the connections reported; each
	// goroutine has already filtered out its own clean shutdown, so anything
	// left is a real failure.
	err := g.Wait()
	if ctx.Err() != nil {
		// Shutting down because the caller asked is not a failure, whatever
		// the connections reported as they were torn down.
		return nil
	}
	return err
}

func formatForwards(forwards []ForwardConfig) string {
	if len(forwards) == 0 {
		return "none"
	}
	parts := make([]string, len(forwards))
	for i, f := range forwards {
		parts[i] = fmt.Sprintf("127.0.0.1:%d->:%d", f.LocalPort, f.RemotePort)
	}
	return strings.Join(parts, ", ")
}
