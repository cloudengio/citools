// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloudeng.io/net/pssh"
	"gopkg.in/yaml.v3"
)

// parseConfig parses and validates spec, which must be valid.
func parseConfig(t *testing.T, spec string) Config {
	t.Helper()
	var cfg Config
	if err := yaml.Unmarshal([]byte(spec), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestProxyHostByName covers a proxy host that is another connection: it says
// how that host is reached, and is maintained only for as long as the
// connections reached through it are.
func TestProxyHostByName(t *testing.T) {
	cfg := parseConfig(t, `
connections:
  - name: bastion
    host: bastion.example.com
    ssh:
      user: admin
      port: 2222
  - name: db
    host: db.internal
    ssh:
      user: dba
      proxy: bastion
      port_forward:
        forwards: [5432:5432]
`)
	db, err := cfg.Connection("db")
	if err != nil {
		t.Fatal(err)
	}
	hop, ok := db.ProxyHost()
	if !ok {
		t.Fatal("db has no proxy host")
	}
	if got, want := hop.Name, "bastion"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	// The hop is reached as its own connection says, not as db is.
	if got, want := hop.HostPort(), "bastion.example.com:2222"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if got, want := hop.ParsedUser(), "admin"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}

	// A connection that is only a proxy host is not one to connect to in its
	// own right: it is maintained with the connections that use it.
	if !cfg.ProxyHostOnly("bastion") {
		t.Error("bastion should be a proxy host only")
	}
	var maintained []string
	for _, c := range cfg.ConnectConnections() {
		maintained = append(maintained, c.Name)
	}
	if got, want := strings.Join(maintained, ","), "db"; got != want {
		t.Errorf("maintained %v, want %v", got, want)
	}
}

// TestProxyHostAlsoMaintained covers a proxy host that has port forwards of its
// own, which is maintained in its own right as well as carrying others.
func TestProxyHostAlsoMaintained(t *testing.T) {
	cfg := parseConfig(t, `
connections:
  - name: bastion
    host: bastion.example.com
    ssh:
      port_forward:
        forwards: [9090:90]
  - name: db
    host: db.internal
    ssh:
      proxy: bastion
      port_forward:
        forwards: [5432:5432]
`)
	if cfg.ProxyHostOnly("bastion") {
		t.Error("a proxy host with forwards of its own is not a proxy host only")
	}
	var maintained []string
	for _, c := range cfg.ConnectConnections() {
		maintained = append(maintained, c.Name)
	}
	if got, want := strings.Join(maintained, ","), "bastion,db"; got != want {
		t.Errorf("maintained %v, want %v", got, want)
	}
}

// TestProxyHostInline covers a proxy host given as a host rather than by name,
// which has nowhere of its own to say how it is reached and so is reached as
// the connection that names it is.
func TestProxyHostInline(t *testing.T) {
	cfg := parseConfig(t, `
connections:
  - name: db
    host: db.internal
    ssh:
      user: dba
      dial_timeout: 5s
      accept_new_host_keys: true
      known_hosts: [/tmp/known_hosts]
      proxy: admin@gw.example.com:2222
      port_forward:
        forwards: [5432:5432]
`)
	// The inline host is not a connection of its own.
	if got, want := len(cfg.Connections), 1; got != want {
		t.Fatalf("got %v connections, want %v", got, want)
	}
	db := cfg.Connections[0]
	hop, ok := db.ProxyHost()
	if !ok {
		t.Fatal("db has no proxy host")
	}
	if got, want := hop.HostPort(), "gw.example.com:2222"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if got, want := hop.ParsedUser(), "admin"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if got, want := hop.SSH.DialTimeout, 5*time.Second; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if !hop.SSH.AcceptNewHostKeys {
		t.Error("the inline proxy host did not inherit accept_new_host_keys")
	}
	if got, want := strings.Join(hop.SSH.KnownHosts, ","), "/tmp/known_hosts"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestProxyHostInlineHostOnly covers the shortest form of an inline proxy host,
// a hostname on its own, which defaults as any other host does.
func TestProxyHostInlineHostOnly(t *testing.T) {
	cfg := parseConfig(t, `
connections:
  - name: db
    host: db.internal
    ssh:
      proxy: gw.example.com
      port_forward:
        forwards: [5432:5432]
`)
	hop, ok := cfg.Connections[0].ProxyHost()
	if !ok {
		t.Fatal("db has no proxy host")
	}
	if got, want := hop.HostPort(), "gw.example.com:22"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestProxyHostErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec string
		want string
	}{
		{
			// A bare word is a mistyped connection name far more often than
			// it is a hostname, and is reported as one.
			name: "no such connection",
			spec: `
connections:
  - name: db
    host: db.internal
    ssh:
      proxy: basion
`,
			want: `no connection of that name (available: db)`,
		},
		{
			name: "itself",
			spec: `
connections:
  - name: db
    host: db.internal
    ssh:
      proxy: db
`,
			want: `proxy "db" is the connection itself`,
		},
		{
			name: "two hops",
			spec: `
connections:
  - name: outer
    host: outer.example.com
    ssh:
      proxy: gw.example.com
  - name: inner
    host: inner.internal
    ssh:
      proxy: outer
`,
			want: `only one hop is supported`,
		},
		{
			name: "unusable inline host",
			spec: `
connections:
  - name: db
    host: db.internal
    ssh:
      proxy: admin@gw.example.com:0
`,
			want: `invalid port`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg Config
			if err := yaml.Unmarshal([]byte(tc.spec), &cfg); err != nil {
				t.Fatal(err)
			}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestFormatProxy(t *testing.T) {
	cfg := parseConfig(t, `
connections:
  - name: bastion
    host: bastion.example.com
    ssh:
      port_forward:
        forwards: [9090:90]
  - name: db
    host: db.internal
    ssh:
      proxy: bastion
      port_forward:
        forwards: [5432:5432]
`)
	bastion, db := cfg.Connections[0], cfg.Connections[1]
	if got, want := formatProxy(db), "bastion"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if got, want := formatProxy(bastion), "none"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestConnectCommandProxyHostOnly verifies that naming a connection which
// exists only to be proxyed through says why it cannot be connected to rather
// than reporting it as missing.
func TestConnectCommandProxyHostOnly(t *testing.T) {
	cfgPath := writeTestConfig(t, t.TempDir(), `
connections:
  - name: bastion
    host: bastion.example.com
    type: ssh
  - name: db
    host: db.internal
    ssh:
      proxy: bastion
      port_forward:
        forwards: [5432:5432]
`)
	cli := newTestCLI(t)
	err := cli.DispatchWithArgs(t.Context(), "pconn", "--config="+cfgPath, "connect", "bastion")
	if err == nil || !strings.Contains(err.Error(), "is the proxy host of other connections") {
		t.Errorf("got %v, want an error explaining that bastion is only a proxy host", err)
	}
}

// TestConnectCommandProxyHostIntegration connects to two hosts through a proxy
// host: each connection is carried by the proxy host's connection, the port
// forwards run over both, and the two share one connection to the proxy host
// rather than each making its own.
func TestConnectCommandProxyHostIntegration(t *testing.T) {
	userPriv, userSigner := newTestKey(t)
	_, bastionKey := newTestKey(t)
	_, targetKey := newTestKey(t)
	serveTestAgent(t, userPriv)

	const user = "testuser"
	bastion := newTestSSHServer(t, bastionKey, userSigner, user)
	target := newTestSSHServer(t, targetKey, userSigner, user)

	remoteEcho := echoServer(t)
	forwardA, forwardB := freePort(t), freePort(t)

	tmpDir := t.TempDir()
	knownHostsPath := filepath.Join(tmpDir, "known_hosts")
	knownHosts := knownHostsLine(bastion, bastionKey.PublicKey()) +
		knownHostsLine(target, targetKey.PublicKey())
	if err := os.WriteFile(knownHostsPath, []byte(knownHosts), 0600); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(tmpDir, "pconn.log")
	cfgPath := writeTestConfig(t, tmpDir, fmt.Sprintf(`
logging:
  level: 2
  format: text
  file: %s
connections:
  # Reached directly, and carries the two connections below. It has no
  # forwards of its own, so it is maintained only for as long as they are.
  - name: bastion
    host: %s
    ssh:
      user: %s
      known_hosts: [%s]
  - name: behind-a
    host: %s
    ssh:
      user: %s
      known_hosts: [%s]
      proxy: bastion
      port_forward:
        forwards: [%d:%d]
  - name: behind-b
    host: %s
    ssh:
      user: %s
      known_hosts: [%s]
      proxy: bastion
      port_forward:
        forwards: [%d:%d]
`, logPath,
		bastion.addr(), user, knownHostsPath,
		target.addr(), user, knownHostsPath, forwardA, remoteEcho,
		target.addr(), user, knownHostsPath, forwardB, remoteEcho))

	ctx, cancel := context.WithCancel(t.Context())
	cli := newTestCLI(t)
	dispatchDone := make(chan error, 1)
	go func() {
		dispatchDone <- cli.DispatchWithArgs(ctx, "pconn", "--config="+cfgPath, "connect")
	}()

	// The proxy host is connected to although it was not selected, since the
	// connections that are reached through it were.
	for _, srv := range []*testSSHServer{bastion, target} {
		select {
		case err := <-dispatchDone:
			cancel()
			t.Fatalf("dispatch returned early: %v", err)
		case <-srv.accepted:
		case <-time.After(30 * time.Second):
			cancel()
			t.Fatal("timed out waiting for the connection")
		}
	}

	// Each forward runs over the connection to the target, which is itself
	// carried by the connection to the proxy host.
	echoCheck(t, forwardA, "ping-behind-a")
	echoCheck(t, forwardB, "ping-behind-b")

	if got, want := bastion.connCount(), 1; got != want {
		t.Errorf("the proxy host accepted %v connections, want %v: the connections through it should share one", got, want)
	}
	if got, want := target.connCount(), 2; got != want {
		t.Errorf("the target accepted %v connections, want %v", got, want)
	}

	cancel()
	select {
	case err := <-dispatchDone:
		if !isCleanShutdown(err) {
			t.Fatalf("expected a clean shutdown, got: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for the shutdown")
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	// The log has to say which connections go through the proxy host, since
	// that is what says where to look when they cannot be established.
	if want := "proxy=bastion"; !strings.Contains(string(logBytes), want) {
		t.Errorf("the log does not contain %q:\n%s", want, logBytes)
	}
}

// TestTmuxProxyHost covers the proxy host that 'pconn tmux' maintains, which
// is the one thing the connect command does not share with it: the connection
// that tmux attaches over is carried by the proxy host's connection, and the
// proxy host is maintained for as long as the attachment runs.
func TestTmuxProxyHost(t *testing.T) {
	userPriv, userSigner := newTestKey(t)
	_, proxyKey := newTestKey(t)
	_, targetKey := newTestKey(t)
	serveTestAgent(t, userPriv)

	const user = "testuser"
	proxySrv := newTestSSHServer(t, proxyKey, userSigner, user)
	targetSrv := newTestSSHServer(t, targetKey, userSigner, user)

	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	knownHosts := knownHostsLine(proxySrv, proxyKey.PublicKey()) +
		knownHostsLine(targetSrv, targetKey.PublicKey())
	if err := os.WriteFile(knownHostsPath, []byte(knownHosts), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := parseConfig(t, fmt.Sprintf(`
connections:
  - name: bastion
    host: %s
    ssh:
      user: %s
      known_hosts: [%s]
  - name: build-box
    host: %s
    ssh:
      user: %s
      known_hosts: [%s]
      proxy: bastion
      tmux:
        command: tmux -CC new -A -s main
`, proxySrv.addr(), user, knownHostsPath, targetSrv.addr(), user, knownHostsPath))

	tf := &TmuxFlags{KeepAlive: time.Second}
	// The failures of either connection are reported to the terminal by the
	// command itself, which has none here.
	silent := func(string) func(error, bool) { return func(error, bool) {} }

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// A connection that is reached directly has no proxy host to maintain.
	bastion, err := cfg.Connection("bastion")
	if err != nil {
		t.Fatal(err)
	}
	if client, stop := runProxyHost(ctx, bastion, tf, silent); client != nil || stop() != nil {
		t.Errorf("got %v, want no proxy host for a connection that has none", client)
	}

	// The connection that is reached through it is built as the tmux command
	// builds it, and is carried by the proxy host's connection.
	conn, err := cfg.Connection("build-box")
	if err != nil {
		t.Fatal(err)
	}
	proxyClient, stopProxy := runProxyHost(ctx, conn, tf, silent)
	if proxyClient == nil {
		t.Fatal("build-box is reached through bastion, so it has a proxy host")
	}
	client := pssh.NewClient(ctx, "tcp", conn.HostPort(),
		append(identityOptions(conn, conn.SSH.Tmux.ForwardAgent),
			pssh.WithDialTimeout(conn.SSH.DialTimeout),
			pssh.WithKeepAlive(tf.KeepAlive),
			pssh.WithProxyHost(proxyClient))...)
	connDone := make(chan error, 1)
	go func() { connDone <- client.ConnectAndWait(ctx, newBackoffController(true).Backoff) }()

	waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Second)
	defer waitCancel()
	if err := client.WaitConnected(waitCtx); err != nil {
		t.Fatal(err)
	}
	for _, srv := range []*testSSHServer{proxySrv, targetSrv} {
		srv.waitForConnection(t)
	}

	client.Close()
	if err := <-connDone; !errors.Is(err, pssh.ErrClientClosed) {
		t.Errorf("got %v, want %v", err, pssh.ErrClientClosed)
	}
	// Stopping it is a clean shutdown, not a failure to report.
	if err := stopProxy(); err != nil {
		t.Errorf("stopping the proxy host: %v", err)
	}
}

// TestProxyHostStopsWhenDependentsExit verifies that a proxy-only host does
// not keep 'pconn connect' alive once every connection reached through it has
// terminated with an error.
func TestProxyHostStopsWhenDependentsExit(t *testing.T) {
	userPriv, userSigner := newTestKey(t)
	_, bastionKey := newTestKey(t)
	_, wrongTargetKey := newTestKey(t)
	_, targetKey := newTestKey(t)
	serveTestAgent(t, userPriv)

	const user = "testuser"
	bastion := newTestSSHServer(t, bastionKey, userSigner, user)
	target := newTestSSHServer(t, targetKey, userSigner, user)

	tmpDir := t.TempDir()
	knownHostsPath := filepath.Join(tmpDir, "known_hosts")
	// Target presents targetKey, while known_hosts expects wrongTargetKey so
	// the target fails permanently without --fail-fast.
	knownHosts := knownHostsLine(bastion, bastionKey.PublicKey()) +
		knownHostsLine(target, wrongTargetKey.PublicKey())
	if err := os.WriteFile(knownHostsPath, []byte(knownHosts), 0600); err != nil {
		t.Fatal(err)
	}

	cfgPath := writeTestConfig(t, tmpDir, fmt.Sprintf(`
connections:
  - name: bastion
    host: %s
    ssh:
      user: %s
      known_hosts: [%s]
  - name: db
    host: %s
    ssh:
      user: %s
      known_hosts: [%s]
      proxy: bastion
      port_forward:
        forwards: [%d:80]
`, bastion.addr(), user, knownHostsPath, target.addr(), user, knownHostsPath, freePort(t)))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cli := newTestCLI(t)
	err := cli.DispatchWithArgs(ctx, "pconn", "--config="+cfgPath, "connect")
	if err == nil {
		t.Fatal("expected connect to fail when target host key mismatches")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("connect hung after target failed: %v", err)
	}
}
