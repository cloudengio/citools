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
	"sync/atomic"
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
				for _, conn := range cfg.ConnectConnections() {
					if conn.Name == target {
						byName = append(byName, conn)
					}
				}
				if len(byName) == 0 {
					if cfg.ProxyHostOnly(target) {
						return fmt.Errorf("connection %q is the proxy host of other connections and is maintained for as long as they are, rather than being connected to in its own right: name one of them, or give it port forwards of its own", target)
					}
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
			connsToMaintain = cfg.ConnectConnections()
			targetDesc = fmt.Sprintf("all connections (%d connections)", len(connsToMaintain))
		} else {
			connsToMaintain = cfg.ConnectConnections()
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
	proxy      *connRunner
	proxyOnly  bool
	dependents atomic.Int32
}

// newConnRunner creates the client that maintains conn, which is reached over
// the connection that proxy maintains when there is one.
func newConnRunner(
	ctx context.Context,
	reporter *statusReporter,
	logger *slog.Logger,
	conn ConnectionConfig,
	proxy *connRunner,
	cf *ConnectFlags,
) *connRunner {
	hostPort := conn.HostPort()
	connLogger := logger.With("connection", conn.Name, "type", conn.Type)
	unlimited := conn.ShouldUnlimitedRetries(cf.UnlimitedRetries)
	// pssh adds ssh.network and ssh.addr to the logger it is given, so only
	// the waiter's own lines need the address adding.
	waiter := reporter.add(connLogger.With("ssh.addr", hostPort), conn, unlimited)

	psshOpts := []pssh.Option{
		pssh.WithDialTimeout(conn.SSH.DialTimeout),
		pssh.WithInterfaceRecovery(cf.InterfaceRecovery),
		pssh.WithLogger(connLogger),
		pssh.WithOnConnect(func(c *pssh.Client) {
			key, comment := c.AuthKey()
			keyName := comment
			if keyName == "" && key != nil {
				keyName = ssh.FingerprintSHA256(key)
			}
			// The waiter logs this, so that a connection which was reported
			// as pending is reported as established at the same level, and a
			// reconnection as a reconnection.
			waiter.established(keyName)
		}),
		// Without this the reason a connection cannot be made is never
		// reported: pssh retries, so it does not return it.
		pssh.WithOnAttemptFailed(waiter.attemptFailed),
	}
	if proxy != nil {
		// The connection is carried by the proxy host's, so it is established
		// once that one is and lost whenever that one is lost.
		psshOpts = append(psshOpts, pssh.WithProxyHost(proxy.client))
	}
	psshOpts = append(psshOpts, identityOptions(conn, conn.SSH.PortForward.ForwardAgent)...)
	for _, f := range conn.SSH.PortForward.Forwards {
		psshOpts = append(psshOpts, pssh.WithLocalPortForward(f.LocalPort, f.RemotePort))
	}
	return &connRunner{
		conn:       conn,
		client:     pssh.NewClient(ctx, "tcp", hostPort, psshOpts...),
		controller: newBackoffController(unlimited),
		waiter:     waiter,
		proxy:      proxy,
		proxyOnly:  !conn.MaintainedByConnect(),
	}
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
	runners := make([]*connRunner, 0, len(conns))
	byName := make(map[string]*connRunner, len(conns))

	// runnerFor returns the runner that maintains conn, creating it the first
	// time it is needed. Connections that are reached through the same proxy
	// host therefore share one connection to it rather than each making its
	// own, and a proxy host that is a connection in its own right is maintained
	// once rather than twice.
	var runnerFor func(conn ConnectionConfig) (*connRunner, error)
	runnerFor = func(conn ConnectionConfig) (*connRunner, error) {
		if r, ok := byName[conn.Name]; ok {
			return r, nil
		}
		if conn.Type != "ssh" {
			return nil, fmt.Errorf("connection %q: unsupported connection type %q", conn.Name, conn.Type)
		}
		// A proxy host is only ever one hop away, which Config.Validate
		// enforces, so this recurses once at most.
		var proxy *connRunner
		if hop, ok := conn.ProxyHost(); ok {
			var err error
			if proxy, err = runnerFor(hop); err != nil {
				return nil, fmt.Errorf("connection %q: its proxy host: %w", conn.Name, err)
			}
		}
		r := newConnRunner(runCtx, reporter, logger, conn, proxy, cf)
		if proxy != nil && proxy.proxyOnly {
			proxy.dependents.Add(1)
		}
		byName[conn.Name] = r
		// Appended after its proxy host, so that the host is the first of the
		// two to be reported. Nothing requires that order: a connection waits
		// for the one it is reached through to be established.
		runners = append(runners, r)
		return r, nil
	}
	for _, conn := range conns {
		if _, err := runnerFor(conn); err != nil {
			return err
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
			// Closing the client once it is no longer being maintained is what
			// ends the connections reached through it, see pssh.WithProxyHost:
			// a proxy host that has given up must not be retried through.
			defer r.client.Close()
			if r.proxy != nil && r.proxy.proxyOnly {
				defer func() {
					if r.proxy.dependents.Add(-1) == 0 {
						r.proxy.client.Close()
					}
				}()
			}

			hostPort := r.conn.HostPort()
			logger.Info("maintaining persistent connection",
				"connection", r.conn.Name,
				"type", r.conn.Type,
				"host", hostPort,
				"user", r.conn.ParsedUser(),
				"dial_timeout", r.conn.SSH.DialTimeout,
				"unlimited_retries", r.conn.ShouldUnlimitedRetries(cf.UnlimitedRetries),
				"interface_recovery", cf.InterfaceRecovery,
				"forwards", formatForwards(r.conn.SSH.PortForward.Forwards),
				"proxy", formatProxy(r.conn),
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

// identityOptions returns the options that say who to connect as and how to
// verify the server, which are the same however the connection is used.
func identityOptions(conn ConnectionConfig, forwardAgent bool) []pssh.Option {
	var opts []pssh.Option
	if conn.SSH.parsedUser != "" {
		opts = append(opts, pssh.WithUser(conn.SSH.parsedUser))
	}
	if conn.SSH.AcceptNewHostKeys {
		opts = append(opts, pssh.WithAcceptNewHostKeys(true))
	}
	if len(conn.SSH.KnownHosts) > 0 {
		opts = append(opts, pssh.WithKnownHosts(conn.SSH.KnownHosts...))
	}
	if forwardAgent {
		opts = append(opts, pssh.WithAgentForwarding(true))
	}
	return opts
}

// formatProxy names the connection that conn is reached through, for logging.
func formatProxy(conn ConnectionConfig) string {
	if hop, ok := conn.ProxyHost(); ok {
		return hop.Name
	}
	return "none"
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
