// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/subcmd"
	"cloudeng.io/net/pssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// TestIsCleanShutdown covers this command's delegation to
// cmdutil.IsCleanShutdown rather than the predicate itself, which cmdutil
// tests: that pssh.ErrClientClosed is treated as clean alongside the default
// sentinels, and that every error is required to be clean, so that a run which
// was shut down and also failed is still reported as a failure.
func TestIsCleanShutdown(t *testing.T) {
	if !isCleanShutdown(nil) {
		t.Errorf("isCleanShutdown(nil) = false, want true")
	}
	if !isCleanShutdown(context.Canceled) {
		t.Errorf("isCleanShutdown(context.Canceled) = false, want true")
	}
	if !isCleanShutdown(cmdutil.ErrInterrupt) {
		t.Errorf("isCleanShutdown(cmdutil.ErrInterrupt) = false, want true")
	}
	if !isCleanShutdown(pssh.ErrClientClosed) {
		t.Errorf("isCleanShutdown(pssh.ErrClientClosed) = false, want true")
	}
	wrapped := fmt.Errorf("outer: %w", context.Canceled)
	if !isCleanShutdown(wrapped) {
		t.Errorf("isCleanShutdown(wrapped) = false, want true")
	}
	realErr := errors.New("real error")
	if isCleanShutdown(realErr) {
		t.Errorf("isCleanShutdown(realErr) = true, want false")
	}
	if isCleanShutdown(errors.Join(realErr, pssh.ErrClientClosed)) {
		t.Errorf("isCleanShutdown(a failure alongside ErrClientClosed) = true, want false")
	}
}

func TestFormatForwards(t *testing.T) {
	if got, want := formatForwards(nil), "none"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	forwards := []ForwardConfig{
		{LocalPort: 8080, RemotePort: 80},
		{LocalPort: 9090, RemotePort: 9000},
	}
	if got, want := formatForwards(forwards), "127.0.0.1:8080->:80, 127.0.0.1:9090->:9000"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func newTestKey(t *testing.T) (ed25519.PrivateKey, ssh.Signer) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return priv, signer
}

func serveTestAgent(t *testing.T, key ed25519.PrivateKey) {
	t.Helper()
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: key, Comment: "test-key"}); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "pconn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	l, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go agent.ServeAgent(keyring, conn) //nolint:errcheck
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(dir, "s"))
}

type testSSHServer struct {
	listener net.Listener
	accepted chan struct{}
	mu       sync.Mutex
	conns    []net.Conn
}

func newTestSSHServer(t *testing.T, hostSigner, userSigner ssh.Signer, user string) *testSSHServer {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &testSSHServer{
		listener: l,
		accepted: make(chan struct{}, 16),
	}
	t.Cleanup(s.close)

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() != user {
				return nil, fmt.Errorf("got user %q, want %q", c.User(), user)
			}
			if !bytes.Equal(key.Marshal(), userSigner.PublicKey().Marshal()) {
				return nil, fmt.Errorf("unknown public key")
			}
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(hostSigner)

	go func() {
		for {
			conn, err := s.listener.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, conn)
			s.mu.Unlock()

			go func(c net.Conn) {
				_, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				go func() {
					for newChannel := range chans {
						channel, requests, err := newChannel.Accept()
						if err != nil {
							continue
						}
						go ssh.DiscardRequests(requests)
						go func(ch ssh.Channel) {
							defer func() { _ = ch.Close() }()
							_, _ = io.Copy(ch, ch) // echo
						}(channel)
					}
				}()
				select {
				case s.accepted <- struct{}{}:
				default:
				}
			}(conn)
		}
	}()

	return s
}

func (s *testSSHServer) close() {
	_ = s.listener.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		_ = c.Close()
	}
}

func (s *testSSHServer) addr() string {
	return s.listener.Addr().String()
}

func (s *testSSHServer) waitForConnection(t *testing.T) {
	t.Helper()
	select {
	case <-s.accepted:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for SSH connection")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func echoServer(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func echoCheck(t *testing.T, port int, msg string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			_, wErr := io.WriteString(conn, msg)
			buf := make([]byte, len(msg))
			_, rErr := io.ReadFull(conn, buf)
			_ = conn.Close()
			if wErr == nil && rErr == nil && string(buf) == msg {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("port forward on port %d did not echo expected %q within deadline", port, msg)
}

// newTestCLI returns the pconn CLI wired up as main wires it, ie. with the
// package level globalFlags that configPrehook reads, rather than a separate
// struct whose values the pre-hook would never see. globalFlags is reset
// first, so tests using this must not run in parallel with each other.
func newTestCLI(t *testing.T) *subcmd.CommandSetYAML {
	t.Helper()
	globalFlags = GlobalFlags{}
	cli := createCLI()
	cli.WithGlobalFlags(subcmd.NewFlagSet().MustRegisterFlagStruct(&globalFlags, nil, nil))
	return cli
}

// writeTestConfig writes content to pconn.yaml in dir and returns its path.
// Tests that need live ports or host keys must generate their configuration;
// testdata/pconn.yaml covers the example as written.
func writeTestConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "pconn.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write %v: %v", path, err)
	}
	return path
}

func TestConnectCommandIntegration(t *testing.T) {
	userPriv, userSigner := newTestKey(t)
	_, hostSigner := newTestKey(t)
	serveTestAgent(t, userPriv)

	user := "testuser"
	srv := newTestSSHServer(t, hostSigner, userSigner, user)

	remoteEchoPort := echoServer(t)
	localForwardPort := freePort(t)

	// Write known_hosts file
	tmpDir := t.TempDir()
	knownHostsPath := filepath.Join(tmpDir, "known_hosts")
	knownHostsLine := fmt.Sprintf("[%s]:%s %s\n",
		srv.listener.Addr().(*net.TCPAddr).IP.String(),
		srv.listener.Addr().String()[strings.LastIndex(srv.listener.Addr().String(), ":")+1:],
		ssh.MarshalAuthorizedKey(hostSigner.PublicKey()))
	if err := os.WriteFile(knownHostsPath, []byte(knownHostsLine), 0600); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(tmpDir, "pconn.log")
	cfgContent := fmt.Sprintf(`
logging:
  level: 2
  format: text
  file: %s
connections:
  - name: test-conn
    host: %s
    user: %s
    known_hosts:
      - %s
    forwards:
      - local_port: %d
        remote_port: %d
`, logPath, srv.addr(), user, knownHostsPath, localForwardPort, remoteEchoPort)

	cfgPath := writeTestConfig(t, tmpDir, cfgContent)

	ctx, cancel := context.WithCancel(t.Context())
	cli := newTestCLI(t)

	dispatchDone := make(chan error, 1)
	go func() {
		dispatchDone <- cli.DispatchWithArgs(ctx, "pconn", "--config="+cfgPath, "connect")
	}()

	select {
	case err := <-dispatchDone:
		t.Fatalf("dispatch returned early: %v", err)
	case <-srv.accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SSH connection")
	}
	echoCheck(t, localForwardPort, "ping-pconn")

	// Cancel context to initiate clean shutdown
	cancel()

	select {
	case err := <-dispatchDone:
		if !isCleanShutdown(err) {
			t.Fatalf("expected clean shutdown, got: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for dispatch to complete after cancel")
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if !strings.Contains(string(logBytes), "key=test-key") {
		t.Errorf("expected log output to contain key=test-key, got:\n%s", string(logBytes))
	}
}

func TestConnectCommandFilterIntegration(t *testing.T) {
	userPriv, userSigner := newTestKey(t)
	_, hostSigner := newTestKey(t)
	serveTestAgent(t, userPriv)

	user := "testuser"
	srv1 := newTestSSHServer(t, hostSigner, userSigner, user)
	srv2 := newTestSSHServer(t, hostSigner, userSigner, user)

	tmpDir := t.TempDir()
	knownHostsPath := filepath.Join(tmpDir, "known_hosts")
	knownHostsLine := fmt.Sprintf("[%s]:%s %s\n[%s]:%s %s\n",
		srv1.listener.Addr().(*net.TCPAddr).IP.String(),
		srv1.listener.Addr().String()[strings.LastIndex(srv1.listener.Addr().String(), ":")+1:],
		ssh.MarshalAuthorizedKey(hostSigner.PublicKey()),
		srv2.listener.Addr().(*net.TCPAddr).IP.String(),
		srv2.listener.Addr().String()[strings.LastIndex(srv2.listener.Addr().String(), ":")+1:],
		ssh.MarshalAuthorizedKey(hostSigner.PublicKey()))
	if err := os.WriteFile(knownHostsPath, []byte(knownHostsLine), 0600); err != nil {
		t.Fatal(err)
	}

	cfgContent := fmt.Sprintf(`
connections:
  - name: conn1
    host: %s
    user: %s
    known_hosts:
      - %s
  - name: conn2
    host: %s
    user: %s
    known_hosts:
      - %s
`, srv1.addr(), user, knownHostsPath, srv2.addr(), user, knownHostsPath)

	cfgPath := writeTestConfig(t, tmpDir, cfgContent)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cli := newTestCLI(t)

	dispatchDone := make(chan error, 1)
	go func() {
		// Run connect only for conn1
		dispatchDone <- cli.DispatchWithArgs(ctx, "pconn", "--config="+cfgPath, "connect", "conn1")
	}()

	srv1.waitForConnection(t)

	// Ensure srv2 was NOT connected
	select {
	case <-srv2.accepted:
		t.Fatal("conn2 was connected even though only conn1 was requested")
	case <-time.After(300 * time.Millisecond):
		// Expected: srv2 not connected
	}

	cancel()
	err := <-dispatchDone
	if !isCleanShutdown(err) {
		t.Fatalf("expected clean shutdown, got: %v", err)
	}
}

func TestConnectCommandUnknownConnection(t *testing.T) {
	tmpDir := t.TempDir()
	cfgContent := `
connections:
  - name: conn1
    host: 127.0.0.1:22
`
	cfgPath := writeTestConfig(t, tmpDir, cfgContent)

	cli := newTestCLI(t)

	err := cli.DispatchWithArgs(t.Context(), "pconn", "--config="+cfgPath, "connect", "nonexistent")
	if err == nil || (!strings.Contains(err.Error(), "connection(s) not found: nonexistent") && !strings.Contains(err.Error(), "group \"nonexistent\" not found")) {
		t.Fatalf("expected unknown connection/group error, got: %v", err)
	}
}

func TestConnectCommandGroupIntegration(t *testing.T) {
	userPriv, userSigner := newTestKey(t)
	_, hostSigner := newTestKey(t)
	serveTestAgent(t, userPriv)

	user := "testuser"
	srvWeb := newTestSSHServer(t, hostSigner, userSigner, user)
	srvDb := newTestSSHServer(t, hostSigner, userSigner, user)
	srvAnalytics := newTestSSHServer(t, hostSigner, userSigner, user)

	tmpDir := t.TempDir()
	knownHostsPath := filepath.Join(tmpDir, "known_hosts")
	var knownHostsBuf bytes.Buffer
	for _, s := range []*testSSHServer{srvWeb, srvDb, srvAnalytics} {
		fmt.Fprintf(&knownHostsBuf, "[%s]:%s %s\n",
			s.listener.Addr().(*net.TCPAddr).IP.String(),
			s.listener.Addr().String()[strings.LastIndex(s.listener.Addr().String(), ":")+1:],
			ssh.MarshalAuthorizedKey(hostSigner.PublicKey()))
	}
	if err := os.WriteFile(knownHostsPath, knownHostsBuf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}

	cfgContent := fmt.Sprintf(`
connections:
  - name: web
    host: %s
    user: %s
    known_hosts:
      - %s
    groups:
      - dev
      - frontend
  - name: db
    host: %s
    user: %s
    known_hosts:
      - %s
    groups:
      - dev
      - backend
  - name: analytics
    host: %s
    user: %s
    known_hosts:
      - %s
    groups:
      - analytics
`, srvWeb.addr(), user, knownHostsPath, srvDb.addr(), user, knownHostsPath, srvAnalytics.addr(), user, knownHostsPath)

	cfgPath := writeTestConfig(t, tmpDir, cfgContent)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cli := newTestCLI(t)

	dispatchDone := make(chan error, 1)
	go func() {
		// Connect to the 'dev' group (which should connect both web and db, but NOT analytics)
		dispatchDone <- cli.DispatchWithArgs(ctx, "pconn", "--config="+cfgPath, "connect", "dev")
	}()

	// Both web and db must be connected as part of group 'dev'
	srvWeb.waitForConnection(t)
	srvDb.waitForConnection(t)

	// Ensure analytics was NOT connected
	select {
	case <-srvAnalytics.accepted:
		t.Fatal("analytics was connected even though it is not in the 'dev' group")
	case <-time.After(300 * time.Millisecond):
		// Expected
	}

	cancel()
	err := <-dispatchDone
	if !isCleanShutdown(err) {
		t.Fatalf("expected clean shutdown, got: %v", err)
	}
}

func TestConnectCommandGroupFlag(t *testing.T) {
	userPriv, userSigner := newTestKey(t)
	_, hostSigner := newTestKey(t)
	serveTestAgent(t, userPriv)

	user := "testuser"
	srvWeb := newTestSSHServer(t, hostSigner, userSigner, user)
	srvDb := newTestSSHServer(t, hostSigner, userSigner, user)

	tmpDir := t.TempDir()
	knownHostsPath := filepath.Join(tmpDir, "known_hosts")
	var knownHostsBuf bytes.Buffer
	for _, s := range []*testSSHServer{srvWeb, srvDb} {
		fmt.Fprintf(&knownHostsBuf, "[%s]:%s %s\n",
			s.listener.Addr().(*net.TCPAddr).IP.String(),
			s.listener.Addr().String()[strings.LastIndex(s.listener.Addr().String(), ":")+1:],
			ssh.MarshalAuthorizedKey(hostSigner.PublicKey()))
	}
	if err := os.WriteFile(knownHostsPath, knownHostsBuf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}

	cfgContent := fmt.Sprintf(`
connections:
  - name: web
    host: %s
    user: %s
    known_hosts:
      - %s
    groups:
      - dev
  - name: db
    host: %s
    user: %s
    known_hosts:
      - %s
    groups:
      - prod
`, srvWeb.addr(), user, knownHostsPath, srvDb.addr(), user, knownHostsPath)

	cfgPath := writeTestConfig(t, tmpDir, cfgContent)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cli := newTestCLI(t)

	dispatchDone := make(chan error, 1)
	go func() {
		// Use --group=prod flag
		dispatchDone <- cli.DispatchWithArgs(ctx, "pconn", "--config="+cfgPath, "connect", "--group=prod")
	}()

	srvDb.waitForConnection(t)

	select {
	case <-srvWeb.accepted:
		t.Fatal("web was connected even though --group=prod was specified")
	case <-time.After(300 * time.Millisecond):
		// Expected
	}

	cancel()
	err := <-dispatchDone
	if !isCleanShutdown(err) {
		t.Fatalf("expected clean shutdown, got: %v", err)
	}
}

func TestConnectCommandUnknownGroup(t *testing.T) {
	tmpDir := t.TempDir()
	cfgContent := `
connections:
  - name: conn1
    host: 127.0.0.1:22
    groups:
      - dev
      - staging
`
	cfgPath := writeTestConfig(t, tmpDir, cfgContent)

	cli := newTestCLI(t)

	err := cli.DispatchWithArgs(t.Context(), "pconn", "--config="+cfgPath, "connect", "nonexistent")
	if err == nil || !strings.Contains(err.Error(), "group \"nonexistent\" not found in configuration (available groups: dev, staging)") {
		t.Fatalf("expected unknown group error listing dev, staging; got: %v", err)
	}
}

func TestConnectCommandMultipleGroupsAndAll(t *testing.T) {
	userPriv, userSigner := newTestKey(t)
	_, hostSigner := newTestKey(t)
	serveTestAgent(t, userPriv)

	user := "testuser"
	srv1 := newTestSSHServer(t, hostSigner, userSigner, user)
	srv2 := newTestSSHServer(t, hostSigner, userSigner, user)
	srv3 := newTestSSHServer(t, hostSigner, userSigner, user)

	tmpDir := t.TempDir()
	knownHostsPath := filepath.Join(tmpDir, "known_hosts")
	var knownHostsBuf bytes.Buffer
	for _, s := range []*testSSHServer{srv1, srv2, srv3} {
		fmt.Fprintf(&knownHostsBuf, "[%s]:%s %s\n",
			s.listener.Addr().(*net.TCPAddr).IP.String(),
			s.listener.Addr().String()[strings.LastIndex(s.listener.Addr().String(), ":")+1:],
			ssh.MarshalAuthorizedKey(hostSigner.PublicKey()))
	}
	if err := os.WriteFile(knownHostsPath, knownHostsBuf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}

	cfgContent := fmt.Sprintf(`
connections:
  - name: c1
    host: %s
    user: %s
    known_hosts: [%s]
    groups: [g1]
  - name: c2
    host: %s
    user: %s
    known_hosts: [%s]
    groups: [g2]
  - name: c3
    host: %s
    user: %s
    known_hosts: [%s]
    groups: [g3]
`, srv1.addr(), user, knownHostsPath, srv2.addr(), user, knownHostsPath, srv3.addr(), user, knownHostsPath)

	cfgPath := writeTestConfig(t, tmpDir, cfgContent)

	t.Run("multiple groups", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		cli := newTestCLI(t)

		dispatchDone := make(chan error, 1)
		go func() {
			dispatchDone <- cli.DispatchWithArgs(ctx, "pconn", "--config="+cfgPath, "connect", "g1", "g3")
		}()

		srv1.waitForConnection(t)
		srv3.waitForConnection(t)

		select {
		case <-srv2.accepted:
			t.Fatal("c2 was connected even though only g1 and g3 were requested")
		case <-time.After(300 * time.Millisecond):
		}

		cancel()
		if err := <-dispatchDone; !isCleanShutdown(err) {
			t.Fatalf("expected clean shutdown, got: %v", err)
		}
	})

	t.Run("group all", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		cli := newTestCLI(t)

		dispatchDone := make(chan error, 1)
		go func() {
			dispatchDone <- cli.DispatchWithArgs(ctx, "pconn", "--config="+cfgPath, "connect", "all")
		}()

		srv1.waitForConnection(t)
		srv2.waitForConnection(t)
		srv3.waitForConnection(t)

		cancel()
		if err := <-dispatchDone; !isCleanShutdown(err) {
			t.Fatalf("expected clean shutdown, got: %v", err)
		}
	})
}
