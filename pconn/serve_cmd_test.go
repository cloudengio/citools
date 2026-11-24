// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serveFixture is a running 'pconn serve' and what it was given to serve.
type serveFixture struct {
	base      string // http://127.0.0.1:<port>
	forwardA  int
	forwardB  int
	echo      int
	done      <-chan error
	cancel    func()
	cfgPath   string
	logPath   string
	sshServer *testSSHServer
}

// startServe runs 'pconn serve' against an in-process ssh server, with a group
// of two connections and one that belongs to no group, and nothing brought up
// at startup.
func startServe(t *testing.T, args ...string) *serveFixture {
	t.Helper()
	userPriv, userSigner := newTestKey(t)
	_, hostKey := newTestKey(t)
	serveTestAgent(t, userPriv)

	const user = "testuser"
	srv := newTestSSHServer(t, hostKey, userSigner, user)
	echo := echoServer(t)
	forwardA, forwardB, forwardC := freePort(t), freePort(t), freePort(t)

	tmpDir := t.TempDir()
	knownHostsPath := filepath.Join(tmpDir, "known_hosts")
	if err := os.WriteFile(knownHostsPath, []byte(knownHostsLine(srv, hostKey.PublicKey())), 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(tmpDir, "pconn.log")
	cfgPath := writeTestConfig(t, tmpDir, fmt.Sprintf(`
logging:
  level: 2
  format: text
  file: %s
connections:
  - name: ci-web
    host: %s
    ssh:
      user: %s
      known_hosts: [%s]
      port_forward:
        groups: [ci]
        forwards: [%d:%d]
  - name: ci-db
    host: %s
    ssh:
      user: %s
      known_hosts: [%s]
      port_forward:
        groups: [ci]
        forwards:
          - forward: %d:%d
            url: /db
  - name: scratch
    host: %s
    ssh:
      user: %s
      known_hosts: [%s]
      port_forward:
        forwards:
          - forward: %d:%d
            url: none
`, logPath,
		srv.addr(), user, knownHostsPath, forwardA, echo,
		srv.addr(), user, knownHostsPath, forwardB, echo,
		srv.addr(), user, knownHostsPath, forwardC, echo))

	port := freePort(t)
	ctx, cancel := context.WithCancel(t.Context())
	cli := newTestCLI(t)
	done := make(chan error, 1)
	go func() {
		done <- cli.DispatchWithArgs(ctx, "pconn",
			append([]string{"--config=" + cfgPath, "serve",
				fmt.Sprintf("--http=127.0.0.1:%d", port), "--status-interval=0"}, args...)...)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve returned %v, want a clean shutdown", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("serve did not return")
		}
	})

	f := &serveFixture{
		base:     fmt.Sprintf("http://127.0.0.1:%d", port),
		forwardA: forwardA, forwardB: forwardB, echo: echo,
		done: done, cancel: cancel, cfgPath: cfgPath, logPath: logPath, sshServer: srv,
	}
	f.waitServing(t)
	return f
}

// waitServing waits for the page to be served, which happens a moment after
// the command is dispatched.
func (f *serveFixture) waitServing(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if code, _ := f.get(t, "/"); code == http.StatusOK {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the page was never served")
		}
		select {
		case err := <-f.done:
			t.Fatalf("serve returned early: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (f *serveFixture) get(t *testing.T, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(webHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// post asks for a change to the connections, as the page does.
func (f *serveFixture) post(t *testing.T, path, target string, fromPage bool) (int, string) {
	t.Helper()
	url := f.base + path + "?target=" + target
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fromPage {
		req.Header.Set(webHeader, "1")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// waitEstablished waits for every connection of target to be established, as
// the page does before it points a browser at the forwarded ports.
func (f *serveFixture) waitEstablished(t *testing.T, target string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		code, body := f.get(t, "/api/established?target="+target)
		if code != http.StatusOK {
			t.Fatalf("got %v: %v", code, body)
		}
		var state struct {
			Ready   bool   `json:"ready"`
			Waiting string `json:"waiting"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &state); err != nil {
			t.Fatal(err)
		}
		if state.Ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%v was never established: waiting for %v: %v", target, state.Waiting, state.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestServeStartsAndStops covers the page's reason for existing: the
// connections are listed before any of them is running, starting a group
// brings up every connection in it and its forwards carry traffic, and
// stopping it releases the local ports again.
func TestServeStartsAndStops(t *testing.T) {
	f := startServe(t)

	// Everything configured is listed, with its local ports, before anything
	// is connected: that is what the page is for.
	code, body := f.get(t, "/")
	if code != http.StatusOK {
		t.Fatalf("got %v, want %v", code, http.StatusOK)
	}
	for _, want := range []string{
		">ci</span>", ">no group</span>",
		"ci-web", "ci-db", "scratch",
		fmt.Sprintf("localhost:%d", f.forwardA),
		fmt.Sprintf("localhost:%d", f.forwardB),
		`class="status s-stopped"`,
		// The group can be started as a whole, and its web forwards are what
		// the page opens for it.
		`data-start="ci"`,
		// Both of the group's pages are opened when it is started, and the
		// url of the second says where its web interface is served.
		fmt.Sprintf(`data-open="http://127.0.0.1:%d http://127.0.0.1:%d/db"`, f.forwardA, f.forwardB),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not contain %q", want)
		}
	}

	// A request that did not come from the page cannot change anything.
	if code, _ := f.post(t, "/api/start", "ci", false); code != http.StatusForbidden {
		t.Errorf("got %v, want %v for a request from elsewhere", code, http.StatusForbidden)
	}

	if code, body := f.post(t, "/api/start", "ci", true); code != http.StatusOK {
		t.Fatalf("got %v: %v", code, body)
	}
	f.waitEstablished(t, "ci")

	// Both of the group's forwards carry traffic, and the connection that
	// belongs to no group was not started.
	echoCheck(t, f.forwardA, "ping-ci-web")
	echoCheck(t, f.forwardB, "ping-ci-db")
	if _, body := f.get(t, "/"); !strings.Contains(body, `class="status s-up"`) {
		t.Errorf("the page does not report the group as up:\n%v", body)
	}

	// Starting it again is not an error and leaves it alone.
	if code, body := f.post(t, "/api/start", "ci", true); code != http.StatusOK {
		t.Fatalf("starting a group that is up: got %v: %v", code, body)
	}
	echoCheck(t, f.forwardA, "still-up")

	// Stopping releases the ports, which the page has to be able to promise:
	// the next thing it says about them is that they are not forwarded.
	if code, body := f.post(t, "/api/stop", "ci", true); code != http.StatusOK {
		t.Fatalf("got %v: %v", code, body)
	}
	for _, port := range []int{f.forwardA, f.forwardB} {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Errorf("port %v was not released: %v", port, err)
			continue
		}
		_ = l.Close()
	}
	if _, body := f.get(t, "/"); strings.Contains(body, `class="status s-up"`) {
		t.Errorf("the page still reports a connection as up:\n%v", body)
	}
}

// TestServeStartsAtStartup covers the groups named on the command line, which
// are brought up without waiting for the page.
func TestServeStartsAtStartup(t *testing.T) {
	f := startServe(t, "ci")
	f.waitEstablished(t, "ci")
	echoCheck(t, f.forwardA, "ping-at-startup")
}

// TestServeRejects covers what the page is told when it asks for something
// that cannot be done.
func TestServeRejects(t *testing.T) {
	f := startServe(t)
	for _, tc := range []struct {
		target string
		want   string
	}{
		{"nosuch", "no group or connection named"},
		{"", "no group or connection was named"},
	} {
		code, body := f.post(t, "/api/start", tc.target, true)
		if code == http.StatusOK || !strings.Contains(body, tc.want) {
			t.Errorf("%q: got %v %v, want an error containing %q", tc.target, code, body, tc.want)
		}
	}
	// The page is not served from anywhere else.
	if code, _ := f.get(t, "/elsewhere"); code != http.StatusNotFound {
		t.Errorf("got %v, want %v", code, http.StatusNotFound)
	}
}

// TestServePortClash covers both commands refusing to serve the page on a port
// that a connection forwards, which is checked before anything is started:
// the two would bind the same port here and one of them would fail.
func TestServePortClash(t *testing.T) {
	forwarded := freePort(t)
	cfgPath := writeTestConfig(t, t.TempDir(), fmt.Sprintf(`
connections:
  - name: web
    host: web.example.com
    ssh:
      port_forward:
        groups: [ci]
        forwards: [%d:80]
`, forwarded))

	for _, args := range [][]string{
		{"serve", fmt.Sprintf("--http=%d", forwarded)},
		// connect serves the same page, read-only, and the port it is served
		// on is checked against every connection in the configuration and not
		// only those it was asked to maintain.
		{"connect", fmt.Sprintf("--http=%d", forwarded), "--status-interval=0"},
	} {
		cli := newTestCLI(t)
		err := cli.DispatchWithArgs(t.Context(), "pconn", append([]string{"--config=" + cfgPath}, args...)...)
		if err == nil || !strings.Contains(err.Error(), `connection "web" forwards that port`) {
			t.Errorf("%v: got %v, want an error naming the connection that forwards the port", args[0], err)
		}
	}

	// The port is left free by the attempt that was refused.
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", forwarded))
	if err != nil {
		t.Errorf("the address was not released: %v", err)
	} else {
		_ = l.Close()
	}
}
