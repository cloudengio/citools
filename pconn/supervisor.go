// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// supervisor maintains connections that are started and stopped whilst it
// runs, which is what the page served by 'pconn serve' does to them.
// maintainConnections, which 'pconn connect' uses, maintains instead a set
// that is fixed when it starts.
//
// Its methods are called from the server's request handlers as well as from
// the command itself, so they are safe for concurrent use.
type supervisor struct {
	cfg      Config
	opts     connOpts
	logger   *slog.Logger
	reporter *statusReporter
	// ctx is the lifetime of the supervisor: every connection it starts is
	// maintained for as long as this is live.
	ctx context.Context
	wg  sync.WaitGroup

	mu      sync.Mutex
	running map[string]*connRunner
	// failed holds why a connection stopped on its own, which is the only
	// account of a connection that could not be established at all.
	failed map[string]error
}

func newSupervisor(ctx context.Context, cfg Config, opts connOpts, logger *slog.Logger, reporter *statusReporter) *supervisor {
	return &supervisor{
		cfg:      cfg,
		opts:     opts,
		logger:   logger,
		reporter: reporter,
		ctx:      ctx,
		running:  map[string]*connRunner{},
		failed:   map[string]error{},
	}
}

// resolve returns the connections that target selects: a group, the special
// group all, or a single connection by name. A connection that exists only to
// be jumped through is not one to select, as it is not for 'pconn connect'
// either: it is started with whatever is reached through it.
func (s *supervisor) resolve(target string) ([]ConnectionConfig, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("no connection or group was named")
	}
	if conns, err := s.cfg.ConnectionsForGroup(target); err == nil {
		return conns, nil
	}
	conn, err := s.cfg.Connection(target)
	if err != nil {
		return nil, fmt.Errorf("no group or connection named %q", target)
	}
	// Before the check below, which a proxy host also fails: being one is the
	// more specific reason, and the one worth being told.
	if s.cfg.ProxyHostOnly(target) {
		return nil, fmt.Errorf("%q is the proxy host of other connections and is started with them", target)
	}
	if !conn.MaintainedByConnect() {
		return nil, fmt.Errorf("%q has no port forwards to maintain: it is for 'pconn tmux'", target)
	}
	return []ConnectionConfig{conn}, nil
}

// Start starts the connections that each target selects, and returns once each
// of them is being maintained, which is not the same as being established: that
// is waited for by whatever needs the forwards, see WaitEstablished.
//
// Connections that are already running are left as they are, so starting a
// group twice is not an error and does not disturb what is up.
func (s *supervisor) Start(targets ...string) error {
	var conns []ConnectionConfig
	for _, t := range targets {
		matched, err := s.resolve(t)
		if err != nil {
			return err
		}
		conns = append(conns, matched...)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, conn := range conns {
		if err := s.startLocked(conn); err != nil {
			return err
		}
	}
	return nil
}

// startLocked starts one connection and the proxy host it is reached through.
// It must be called with the lock held.
func (s *supervisor) startLocked(conn ConnectionConfig) error {
	if _, ok := s.running[conn.Name]; ok {
		return nil
	}
	if conn.Type != "ssh" {
		return fmt.Errorf("connection %q: unsupported connection type %q", conn.Name, conn.Type)
	}
	// A local port that something else has already bound would fail
	// asynchronously, as a dial failure that cannot be retried, so it is
	// reported here instead where it can be acted on.
	if port, owner, ok := s.boundPortLocked(conn); ok {
		return fmt.Errorf("connection %q cannot be started: local port %d is already forwarded by %q", conn.Name, port, owner)
	}
	// A proxy host is only ever one hop away, which Config.Validate enforces,
	// so this recurses once at most.
	var proxy *connRunner
	if hop, ok := conn.ProxyHost(); ok {
		if err := s.startLocked(hop); err != nil {
			return fmt.Errorf("connection %q: its proxy host: %w", conn.Name, err)
		}
		proxy = s.running[hop.Name]
	}
	r := newConnRunner(s.ctx, s.reporter, s.logger, conn, proxy, s.opts)
	if proxy != nil && proxy.proxyOnly {
		proxy.dependents.Add(1)
	}
	s.running[conn.Name] = r
	delete(s.failed, conn.Name)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// Closed last, so that a caller waiting for the connection to have
		// stopped finds its ports released, see connRunner.stopped.
		defer close(r.done)
		defer r.controller.Stop()
		// Closing the client once it is no longer being maintained is what
		// ends the connections reached through it, see pssh.WithProxyHost.
		defer r.client.Close()
		err := runConnection(s.ctx, s.logger, r, s.opts)
		s.finished(r, err)
	}()
	return nil
}

// boundPortLocked reports whether a local port that conn forwards is already
// forwarded by a connection that is running, and by which.
func (s *supervisor) boundPortLocked(conn ConnectionConfig) (int, string, bool) {
	for _, f := range conn.SSH.PortForward.Forwards {
		for _, r := range s.running {
			for _, have := range r.conn.SSH.PortForward.Forwards {
				if have.LocalPort() == f.LocalPort() {
					return f.LocalPort(), r.conn.Name, true
				}
			}
		}
	}
	return 0, "", false
}

// finished records that a connection is no longer being maintained, so that it
// can be started again, and releases the proxy host it was the last user of.
func (s *supervisor) finished(r *connRunner, err error) {
	s.mu.Lock()
	// Only if it is still the current runner for that name: a connection that
	// was stopped and started again has a new one, which this must not evict.
	if have, ok := s.running[r.conn.Name]; ok && have == r {
		delete(s.running, r.conn.Name)
		if err != nil {
			s.failed[r.conn.Name] = err
		}
	}
	s.mu.Unlock()

	s.reporter.remove(r.waiter)
	if r.proxy != nil && r.proxy.proxyOnly && r.proxy.dependents.Add(-1) == 0 {
		// Nothing is reached through it any more, and it has no forwards of
		// its own, so there is nothing left for it to carry.
		r.proxy.client.Close()
	}
}

// Stop stops the connections that each target selects and waits for them to
// have stopped, which is what releases their local ports: a page that reports
// a port as free has to mean it.
func (s *supervisor) Stop(targets ...string) error {
	var stopping []*connRunner
	s.mu.Lock()
	for _, t := range targets {
		conns, err := s.resolve(t)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		for _, conn := range conns {
			if r, ok := s.running[conn.Name]; ok {
				stopping = append(stopping, r)
			}
		}
	}
	s.mu.Unlock()

	for _, r := range stopping {
		// Closing the client is what releases ConnectAndWait, which is what
		// ends the goroutine that is maintaining it.
		r.client.Close()
	}
	for _, r := range stopping {
		<-r.stopped()
	}
	return nil
}

// StopAll stops everything and waits for it, which is how serving ends.
func (s *supervisor) StopAll() {
	s.mu.Lock()
	for _, r := range s.running {
		r.client.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// Running reports whether the named connection is being maintained.
func (s *supervisor) Running(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.running[name]
	return ok
}

// status is what is known about one configured connection: whether it is being
// maintained, how it is getting on if it is, and why it stopped if it did.
type status struct {
	Running bool
	State   connState
	Err     error
}

// Status returns the state of every connection in the configuration, by name,
// so that the page can list what is available as well as what is running.
func (s *supervisor) Status() map[string]status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]status, len(s.cfg.Connections))
	for _, conn := range s.cfg.Connections {
		st := status{Err: s.failed[conn.Name]}
		if r, ok := s.running[conn.Name]; ok {
			st.Running = true
			st.State = r.waiter.state()
		}
		out[conn.Name] = st
	}
	return out
}

// Established reports whether every connection that target selects is
// established, and names one that is not along with why, so that the caller
// can say what it is waiting for.
func (s *supervisor) Established(target string) (string, error) {
	conns, err := s.resolve(target)
	if err != nil {
		return "", err
	}
	byName := s.Status()
	for _, conn := range conns {
		st, ok := byName[conn.Name]
		switch {
		case !ok || (!st.Running && st.Err == nil):
			return conn.Name, fmt.Errorf("connection %q is not running", conn.Name)
		case st.Err != nil:
			return conn.Name, st.Err
		case st.State.Status != "up":
			if st.State.LastErr != nil {
				return conn.Name, fmt.Errorf("%v: %w", st.State.Status, st.State.LastErr)
			}
			return conn.Name, nil
		}
	}
	return "", nil
}

// Groups returns the groups of the configuration in order, each with the
// connections that belong to it, and the connections that belong to none.
func (s *supervisor) Groups() ([]string, map[string][]ConnectionConfig, []ConnectionConfig) {
	byGroup := map[string][]ConnectionConfig{}
	var ungrouped []ConnectionConfig
	for _, conn := range s.cfg.Connections {
		if !conn.MaintainedByConnect() {
			// A connection that is only a proxy host or only tmux is not one
			// to start from the page; a proxy host is started with whatever
			// is reached through it.
			continue
		}
		groups := conn.SSH.PortForward.Groups
		if len(groups) == 0 {
			ungrouped = append(ungrouped, conn)
			continue
		}
		for _, g := range groups {
			byGroup[g] = append(byGroup[g], conn)
		}
	}
	names := make([]string, 0, len(byGroup))
	for name := range byGroup {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, byGroup, ungrouped
}
