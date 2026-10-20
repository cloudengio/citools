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
	"cloudeng.io/cmdutil/cmdyaml"
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
	ConfigFile       string `subcmd:"config,,path to YAML configuration file (overrides global --config)"`
	Group            string `subcmd:"group,,comma-separated list of groups of connections to connect"`
	UnlimitedRetries bool   `subcmd:"unlimited-retries,true,continue retrying connections indefinitely"`
	FailFast         bool   `subcmd:"fail-fast,false,exit immediately if any connection fails permanently"`
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

// Run executes the connect command.
func (c ConnectCommand) Run(ctx context.Context, flags any, args []string) error {
	cf := flags.(*ConnectFlags)

	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return fmt.Errorf("no configuration found in context")
	}

	if cf.ConfigFile != "" && cf.ConfigFile != globalFlags.ConfigFile {
		var newCfg Config
		if err := cmdyaml.ParseConfigFilesStrict(ctx, &newCfg, cf.ConfigFile); err != nil {
			return fmt.Errorf("failed to read configuration file %q: %w", cf.ConfigFile, err)
		}
		if err := newCfg.Validate(); err != nil {
			return fmt.Errorf("configuration file %q is invalid: %w", cf.ConfigFile, err)
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
	return maintainConnections(ctx, logger, connsToMaintain, cf.UnlimitedRetries, cf.FailFast)
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
}

func maintainConnections(
	ctx context.Context,
	logger *slog.Logger,
	conns []ConnectionConfig,
	defaultUnlimitedRetries bool,
	failFast bool,
) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	runners := make([]*connRunner, len(conns))
	for i, conn := range conns {
		switch conn.Type {
		case "ssh":
			hostPort := conn.HostPort()
			connLogger := logger.With("connection", conn.Name, "type", conn.Type, "ssh.addr", hostPort)

			var psshOpts []pssh.Option
			psshOpts = append(psshOpts,
				pssh.WithDialTimeout(conn.DialTimeout),
				pssh.WithLogger(connLogger),
				pssh.WithOnConnect(func(c *pssh.Client) {
					key, comment := c.AuthKey()
					keyName := comment
					if keyName == "" && key != nil {
						keyName = ssh.FingerprintSHA256(key)
					}
					logger.Info("connection established",
						"connection", conn.Name,
						"type", conn.Type,
						"host", hostPort,
						"key", keyName,
					)
				}),
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
			unlimited := conn.ShouldUnlimitedRetries(defaultUnlimitedRetries)
			controller := newBackoffController(unlimited)

			runners[i] = &connRunner{
				conn:       conn,
				client:     client,
				controller: controller,
			}
		default:
			return fmt.Errorf("connection %q: unsupported connection type %q", conn.Name, conn.Type)
		}
	}

	var g *errgroup.T
	if failFast {
		// WithCancel cancels runCtx as soon as one connection fails, which is
		// what --fail-fast asks for.
		g = errgroup.WithCancel(cancel)
	} else {
		// A plain T lets every connection run to completion and collects all
		// of their errors.
		g = &errgroup.T{}
	}

	for _, r := range runners {
		g.Go(func() error {
			defer r.controller.Stop()

			hostPort := r.conn.HostPort()
			logger.Info("maintaining persistent connection",
				"connection", r.conn.Name,
				"type", r.conn.Type,
				"host", hostPort,
				"forwards", formatForwards(r.conn.Forwards),
			)

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
