// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestParseSSHTarget(t *testing.T) {
	tests := []struct {
		target            string
		defaultUser       string
		defaultSSHPort    int
		defaultLocalPort  int
		defaultRemotePort int
		wantUser          string
		wantHost          string
		wantSSHPort       int
		wantLocalPort     int
		wantRemotePort    int
		wantErr           bool
	}{
		{
			target:            "example.com",
			defaultUser:       "",
			defaultSSHPort:    22,
			defaultLocalPort:  0,
			defaultRemotePort: 8088,
			wantUser:          "",
			wantHost:          "example.com",
			wantSSHPort:       22,
			wantLocalPort:     0,
			wantRemotePort:    8088,
		},
		{
			target:            "8080:example.com:9090",
			defaultUser:       "",
			defaultSSHPort:    22,
			defaultLocalPort:  0,
			defaultRemotePort: 8088,
			wantUser:          "",
			wantHost:          "example.com",
			wantSSHPort:       22,
			wantLocalPort:     8080,
			wantRemotePort:    9090,
		},
		{
			target:            ":example.com:9090",
			defaultUser:       "",
			defaultSSHPort:    22,
			defaultLocalPort:  0,
			defaultRemotePort: 8088,
			wantUser:          "",
			wantHost:          "example.com",
			wantSSHPort:       22,
			wantLocalPort:     0,
			wantRemotePort:    9090,
		},
		{
			target:            "8080:example.com:",
			defaultUser:       "",
			defaultSSHPort:    22,
			defaultLocalPort:  0,
			defaultRemotePort: 8088,
			wantUser:          "",
			wantHost:          "example.com",
			wantSSHPort:       22,
			wantLocalPort:     8080,
			wantRemotePort:    8088,
		},
		{
			target:            "admin@8080:example.com:9090",
			defaultUser:       "",
			defaultSSHPort:    22,
			defaultLocalPort:  0,
			defaultRemotePort: 8088,
			wantUser:          "admin",
			wantHost:          "example.com",
			wantSSHPort:       22,
			wantLocalPort:     8080,
			wantRemotePort:    9090,
		},
		{
			target:            "8080:admin@example.com:9090",
			defaultUser:       "",
			defaultSSHPort:    22,
			defaultLocalPort:  0,
			defaultRemotePort: 8088,
			wantUser:          "admin",
			wantHost:          "example.com",
			wantSSHPort:       22,
			wantLocalPort:     8080,
			wantRemotePort:    9090,
		},
		{
			target:  "8080:example.com:2222:9090",
			wantErr: true,
		},
		{
			target:            "8080:[2001:db8::1]:9090",
			defaultUser:       "",
			defaultSSHPort:    22,
			defaultLocalPort:  0,
			defaultRemotePort: 8088,
			wantUser:          "",
			wantHost:          "2001:db8::1",
			wantSSHPort:       22,
			wantLocalPort:     8080,
			wantRemotePort:    9090,
		},
		{
			target:            "[2001:db8::1]:9090",
			defaultUser:       "",
			defaultSSHPort:    22,
			defaultLocalPort:  0,
			defaultRemotePort: 8088,
			wantUser:          "",
			wantHost:          "2001:db8::1",
			wantSSHPort:       22,
			wantLocalPort:     0,
			wantRemotePort:    9090,
		},
		{
			target:            "8080:[2001:db8::1]",
			defaultUser:       "",
			defaultSSHPort:    22,
			defaultLocalPort:  0,
			defaultRemotePort: 8088,
			wantUser:          "",
			wantHost:          "2001:db8::1",
			wantSSHPort:       22,
			wantLocalPort:     8080,
			wantRemotePort:    8088,
		},
		{
			target:            "example.com:2222",
			defaultUser:       "",
			defaultSSHPort:    22,
			defaultLocalPort:  0,
			defaultRemotePort: 8088,
			wantUser:          "",
			wantHost:          "example.com",
			wantSSHPort:       22,
			wantLocalPort:     0,
			wantRemotePort:    2222,
		},
		{
			target:            "example.com:9090",
			defaultUser:       "",
			defaultSSHPort:    22,
			defaultLocalPort:  0,
			defaultRemotePort: 8088,
			wantUser:          "",
			wantHost:          "example.com",
			wantSSHPort:       22,
			wantLocalPort:     0,
			wantRemotePort:    9090,
		},
		{
			target:            "8080:example.com",
			defaultUser:       "",
			defaultSSHPort:    22,
			defaultLocalPort:  0,
			defaultRemotePort: 8088,
			wantUser:          "",
			wantHost:          "example.com",
			wantSSHPort:       22,
			wantLocalPort:     8080,
			wantRemotePort:    8088,
		},
		{
			target:            "admin@example.com",
			defaultUser:       "",
			defaultSSHPort:    22,
			defaultLocalPort:  0,
			defaultRemotePort: 8088,
			wantUser:          "admin",
			wantHost:          "example.com",
			wantSSHPort:       22,
			wantLocalPort:     0,
			wantRemotePort:    8088,
		},
		{
			target:            "example.com",
			defaultUser:       "default-user",
			defaultSSHPort:    222,
			defaultLocalPort:  3000,
			defaultRemotePort: 4000,
			wantUser:          "default-user",
			wantHost:          "example.com",
			wantSSHPort:       222,
			wantLocalPort:     3000,
			wantRemotePort:    4000,
		},
		{
			target:            "8080:example.com:9090",
			defaultUser:       "default-user",
			defaultSSHPort:    222,
			defaultLocalPort:  3000,
			defaultRemotePort: 4000,
			wantUser:          "default-user",
			wantHost:          "example.com",
			wantSSHPort:       222,
			wantLocalPort:     8080,
			wantRemotePort:    9090,
		},
		{
			target:            "8080:example.com",
			defaultUser:       "default-user",
			defaultSSHPort:    222,
			defaultLocalPort:  0,
			defaultRemotePort: 9999,
			wantUser:          "default-user",
			wantHost:          "example.com",
			wantSSHPort:       222,
			wantLocalPort:     8080,
			wantRemotePort:    9999,
		},
		{
			target:  "",
			wantErr: true,
		},
		{
			target:  "user@",
			wantErr: true,
		},
		{
			target:  "8080::8088",
			wantErr: true,
		},
		{
			target:  "invalid:host:8088",
			wantErr: true,
		},
		{
			target:  "8080:host:invalid",
			wantErr: true,
		},
		{
			target:  "8080:9090",
			wantErr: true,
		},
		{
			target:  "foo:bar",
			wantErr: true,
		},
		{
			target:  "8080:[2001:db8::1]:22:9090",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		res, err := parseSSHTarget(tc.target, tc.defaultUser, tc.defaultSSHPort, tc.defaultLocalPort, tc.defaultRemotePort)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseSSHTarget(%q) expected error, got nil", tc.target)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseSSHTarget(%q) unexpected error: %v", tc.target, err)
		}
		if res.user != tc.wantUser {
			t.Errorf("parseSSHTarget(%q) user = %q, want %q", tc.target, res.user, tc.wantUser)
		}
		if res.host != tc.wantHost {
			t.Errorf("parseSSHTarget(%q) host = %q, want %q", tc.target, res.host, tc.wantHost)
		}
		if res.sshPort != tc.wantSSHPort {
			t.Errorf("parseSSHTarget(%q) sshPort = %d, want %d", tc.target, res.sshPort, tc.wantSSHPort)
		}
		if res.localPort != tc.wantLocalPort {
			t.Errorf("parseSSHTarget(%q) localPort = %d, want %d", tc.target, res.localPort, tc.wantLocalPort)
		}
		if res.remotePort != tc.wantRemotePort {
			t.Errorf("parseSSHTarget(%q) remotePort = %d, want %d", tc.target, res.remotePort, tc.wantRemotePort)
		}
	}
}

func TestSelectLocalPort(t *testing.T) {
	// Explicit requested port.
	p, err := selectLocalPort(9999, 8088)
	if err != nil {
		t.Fatalf("selectLocalPort(9999, 8088) error: %v", err)
	}
	if p != 9999 {
		t.Errorf("selectLocalPort(9999, 8088) = %d, want 9999", p)
	}

	// 0 requested, free port picked.
	p, err = selectLocalPort(0, 0)
	if err != nil {
		t.Fatalf("selectLocalPort(0, 0) error: %v", err)
	}
	if p <= 0 {
		t.Errorf("selectLocalPort(0, 0) returned invalid port %d", p)
	}

	// When remotePort is already occupied locally, selectLocalPort must pick another free port.
	occupiedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupiedListener.Close() }()
	occupiedPort := occupiedListener.Addr().(*net.TCPAddr).Port

	p, err = selectLocalPort(0, occupiedPort)
	if err != nil {
		t.Fatalf("selectLocalPort(0, %d) error: %v", occupiedPort, err)
	}
	if p == occupiedPort {
		t.Errorf("selectLocalPort returned already occupied port %d", occupiedPort)
	}
	if p <= 0 {
		t.Errorf("selectLocalPort returned invalid port %d", p)
	}
}

func TestViewCommandArgsValidation(t *testing.T) {
	cmd := ViewCommand{}
	ctx := context.Background()
	flags := &ViewFlags{}

	if err := cmd.Run(ctx, flags, nil); err == nil {
		t.Error("expected error when no args provided, got nil")
	}

	if err := cmd.Run(ctx, flags, []string{"host1", "host2"}); err == nil {
		t.Error("expected error when multiple args provided, got nil")
	}
}

// TestViewCommandEndToEnd exercises the full port-forwarding and browser
// launch flow against an in-process SSH server and in-process SSH agent.
func TestViewCommandEndToEnd(t *testing.T) {
	// 1. Start a simulated remote Web UI HTTP server.
	webListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = webListener.Close() }()
	webPort := webListener.Addr().(*net.TCPAddr).Port

	webServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("orchestrator-web-ui-ok"))
		}),
	}
	go func() {
		_ = webServer.Serve(webListener)
	}()
	defer func() { _ = webServer.Close() }()

	// 2. Generate SSH user and host keys.
	_, userPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	userSigner, err := ssh.NewSignerFromKey(userPriv)
	if err != nil {
		t.Fatal(err)
	}

	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Start an in-process SSH agent on a Unix socket.
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: userPriv}); err != nil {
		t.Fatal(err)
	}
	tmpDir, err := os.MkdirTemp("", "pssh-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	agentListener, err := net.Listen("unix", filepath.Join(tmpDir, "agent.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = agentListener.Close() }()
	go func() {
		for {
			conn, err := agentListener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = agent.ServeAgent(keyring, conn)
			}()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(tmpDir, "agent.sock"))

	// 4. Start an in-process SSH server with direct-tcpip port forwarding support.
	sshListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sshListener.Close() }()

	sshConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if !bytes.Equal(key.Marshal(), userSigner.PublicKey().Marshal()) {
				return nil, fmt.Errorf("unknown user key")
			}
			return &ssh.Permissions{}, nil
		},
	}
	sshConfig.AddHostKey(hostSigner)

	go func() {
		for {
			conn, err := sshListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sconn, chans, reqs, err := ssh.NewServerConn(c, sshConfig)
				if err != nil {
					_ = c.Close()
					return
				}
				defer func() { _ = sconn.Close() }()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "direct-tcpip" {
						_ = nc.Reject(ssh.UnknownChannelType, "only direct-tcpip")
						continue
					}
					var req struct {
						DestAddr string
						DestPort uint32
						OrigAddr string
						OrigPort uint32
					}
					if err := ssh.Unmarshal(nc.ExtraData(), &req); err != nil {
						_ = nc.Reject(ssh.ConnectionFailed, err.Error())
						continue
					}
					target, err := net.Dial("tcp", net.JoinHostPort(req.DestAddr, strconv.Itoa(int(req.DestPort))))
					if err != nil {
						_ = nc.Reject(ssh.ConnectionFailed, err.Error())
						continue
					}
					ch, creqs, err := nc.Accept()
					if err != nil {
						_ = target.Close()
						continue
					}
					go ssh.DiscardRequests(creqs)
					go func() {
						defer func() { _ = target.Close() }()
						defer func() { _ = ch.Close() }()
						_, _ = io.Copy(target, ch)
					}()
					go func() {
						defer func() { _ = target.Close() }()
						defer func() { _ = ch.Close() }()
						_, _ = io.Copy(ch, target)
					}()
				}
			}(conn)
		}
	}()

	// 5. Intercept browser opening.
	openedCh := make(chan string, 1)

	oldOpenBrowser := openBrowserFn
	defer func() { openBrowserFn = oldOpenBrowser }()
	openBrowserFn = func(u string) error {
		openedCh <- u
		return nil
	}

	// 6. Run ViewCommand in the background.
	sshHost, sshPortStr, err := net.SplitHostPort(sshListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	sshPort, err := strconv.Atoi(sshPortStr)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cmd := ViewCommand{}
	flags := &ViewFlags{
		RemotePort:        8088, // default remote port overridden by positional arg below
		LocalPort:         0,
		SSHPort:           sshPort,
		AcceptNewHostKeys: true,
		DialTimeout:       5 * time.Second,
	}

	runErrCh := make(chan error, 1)
	go func() {
		// Pass host:remote-port to verify positional target grammar.
		runErrCh <- cmd.Run(ctx, flags, []string{fmt.Sprintf("%s:%d", sshHost, webPort)})
	}()

	// 7. Wait for browser to open with the forwarded URL.
	var targetURL string
	select {
	case targetURL = <-openedCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for browser to open")
	}

	if targetURL == "" {
		t.Fatal("opened URL is empty")
	}

	// 8. Fetch from the forwarded URL to confirm web traffic reaches the mock web UI.
	resp, err := http.Get(targetURL)
	if err != nil {
		t.Fatalf("HTTP GET %s failed: %v", targetURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	if got, want := string(body), "orchestrator-web-ui-ok"; got != want {
		t.Errorf("got response %q, want %q", got, want)
	}

	// 9. Cancel context to terminate ViewCommand.
	cancel()

	select {
	case err := <-runErrCh:
		if err != nil {
			t.Errorf("ViewCommand.Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ViewCommand.Run to exit")
	}
}

func TestViewCommandTimeoutUnreachableHost(t *testing.T) {
	// Listen on an ephemeral port and close immediately to get an unreachable port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	browserOpened := false
	oldOpenBrowser := openBrowserFn
	defer func() { openBrowserFn = oldOpenBrowser }()
	openBrowserFn = func(_ string) error {
		browserOpened = true
		return nil
	}

	cmd := ViewCommand{}
	flags := &ViewFlags{
		RemotePort:  8088,
		LocalPort:   0,
		SSHPort:     closedPort,
		DialTimeout: 200 * time.Millisecond,
		NoBrowser:   false,
	}

	start := time.Now()
	err = cmd.Run(t.Context(), flags, []string{"127.0.0.1"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected timeout error for unreachable host, got nil")
	}
	if browserOpened {
		t.Errorf("browser should not be opened when connection times out")
	}
	if elapsed > 5*time.Second {
		t.Errorf("expected command to fail within dial timeout (~200ms), took %v", elapsed)
	}
}

func TestViewCommandStrictHostKeyVerificationByDefault(t *testing.T) {
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}

	sshListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sshListener.Close() }()

	sshConfig := &ssh.ServerConfig{}
	sshConfig.AddHostKey(hostSigner)

	go func() {
		for {
			conn, err := sshListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sconn, chans, reqs, err := ssh.NewServerConn(c, sshConfig)
				if err != nil {
					_ = c.Close()
					return
				}
				_ = sconn.Close()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					_ = nc.Reject(ssh.UnknownChannelType, "rejected")
				}
			}(conn)
		}
	}()

	sshHost, sshPortStr, err := net.SplitHostPort(sshListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	sshPort, err := strconv.Atoi(sshPortStr)
	if err != nil {
		t.Fatal(err)
	}

	cmd := ViewCommand{}
	flags := &ViewFlags{
		RemotePort:        8088,
		LocalPort:         0,
		SSHPort:           sshPort,
		AcceptNewHostKeys: false, // Strict known-host verification (default)
		DialTimeout:       1 * time.Second,
		NoBrowser:         true,
	}

	err = cmd.Run(t.Context(), flags, []string{sshHost})
	if err == nil {
		t.Fatalf("expected error due to strict known-hosts check rejecting untrusted host key, got nil")
	}
}
