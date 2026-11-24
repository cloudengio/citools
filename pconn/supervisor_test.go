// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestSupervisor returns a supervisor for spec, which must name live
// servers, along with the reporter it registers connections with.
func newTestSupervisor(t *testing.T, spec string) (*supervisor, *statusReporter) {
	t.Helper()
	cfg := parseConfig(t, spec)
	logger := slog.New(slog.DiscardHandler)
	reporter := newStatusReporter(logger, 0)
	sup := newSupervisor(t.Context(), cfg, connOpts{unlimitedRetries: true}, logger, reporter)
	t.Cleanup(sup.StopAll)
	return sup, reporter
}

// waitEstablished waits for every connection of target to be established.
func waitEstablished(t *testing.T, sup *supervisor, target string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		waiting, err := sup.Established(target)
		if waiting == "" && err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%v was never established: waiting for %v: %v", target, waiting, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// liveConfig writes a configuration whose connections all reach srv, with the
// forwards given as <local>:<remote> pairs per connection.
func liveConfig(t *testing.T, srv *testSSHServer, hostKey string, body string) string {
	t.Helper()
	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHostsPath, []byte(hostKey), 0600); err != nil {
		t.Fatal(err)
	}
	return strings.NewReplacer(
		"$ADDR", srv.addr(),
		"$KNOWN", knownHostsPath,
	).Replace(body)
}

// TestSupervisorStartStop covers starting and stopping connections whilst the
// supervisor runs, which is what the page served by 'pconn serve' does: a
// connection that was stopped can be started again, and stopping it is what
// releases its local port.
func TestSupervisorStartStop(t *testing.T) {
	userPriv, userSigner := newTestKey(t)
	_, hostSigner := newTestKey(t)
	serveTestAgent(t, userPriv)
	srv := newTestSSHServer(t, hostSigner, userSigner, "testuser")
	echo := echoServer(t)
	local := freePort(t)

	sup, reporter := newTestSupervisor(t, liveConfig(t, srv, knownHostsLine(srv, hostSigner.PublicKey()), fmt.Sprintf(`
connections:
  - name: one
    host: $ADDR
    ssh:
      user: testuser
      known_hosts: [$KNOWN]
      port_forward:
        groups: [ci]
        forwards: [%d:%d]
`, local, echo)))

	if sup.Running("one") {
		t.Error("nothing is started until it is asked for")
	}
	if err := sup.Start("ci"); err != nil {
		t.Fatal(err)
	}
	waitEstablished(t, sup, "ci")
	if !sup.Running("one") {
		t.Error("the connection should be running")
	}
	echoCheck(t, local, "up")

	// The reporter reports on what is running, so a connection that has been
	// started is registered with it.
	if got, want := len(reporter.registered()), 1; got != want {
		t.Errorf("got %v registered, want %v", got, want)
	}

	if err := sup.Stop("one"); err != nil {
		t.Fatal(err)
	}
	if sup.Running("one") {
		t.Error("the connection should have stopped")
	}
	// Stop waits for the port to have been released, which is what lets the
	// page say that it is no longer forwarded.
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", local))
	if err != nil {
		t.Errorf("the local port was not released: %v", err)
	} else {
		_ = l.Close()
	}
	// And it is no longer reported on as a connection that is waiting.
	if got, want := len(reporter.registered()), 0; got != want {
		t.Errorf("got %v registered, want %v", got, want)
	}

	// Started again, it works again: the client and its forwards are new.
	if err := sup.Start("one"); err != nil {
		t.Fatal(err)
	}
	waitEstablished(t, sup, "one")
	echoCheck(t, local, "up-again")
}

// TestSupervisorProxyShared covers the proxy host of connections started from
// the page: it is started with the first of them, shared by all of them, and
// stopped with the last, since it exists only to carry them.
func TestSupervisorProxyShared(t *testing.T) {
	userPriv, userSigner := newTestKey(t)
	_, proxyKey := newTestKey(t)
	_, targetKey := newTestKey(t)
	serveTestAgent(t, userPriv)
	proxySrv := newTestSSHServer(t, proxyKey, userSigner, "testuser")
	targetSrv := newTestSSHServer(t, targetKey, userSigner, "testuser")
	echo := echoServer(t)
	first, second := freePort(t), freePort(t)

	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	knownHosts := knownHostsLine(proxySrv, proxyKey.PublicKey()) + knownHostsLine(targetSrv, targetKey.PublicKey())
	if err := os.WriteFile(knownHostsPath, []byte(knownHosts), 0600); err != nil {
		t.Fatal(err)
	}
	sup, _ := newTestSupervisor(t, fmt.Sprintf(`
connections:
  - name: bastion
    host: %s
    ssh:
      user: testuser
      known_hosts: [%s]
  - name: behind-a
    host: %s
    ssh:
      user: testuser
      known_hosts: [%s]
      proxy: bastion
      port_forward:
        forwards: [%d:%d]
  - name: behind-b
    host: %s
    ssh:
      user: testuser
      known_hosts: [%s]
      proxy: bastion
      port_forward:
        forwards: [%d:%d]
`, proxySrv.addr(), knownHostsPath,
		targetSrv.addr(), knownHostsPath, first, echo,
		targetSrv.addr(), knownHostsPath, second, echo))

	if err := sup.Start("behind-a", "behind-b"); err != nil {
		t.Fatal(err)
	}
	waitEstablished(t, sup, "behind-a")
	waitEstablished(t, sup, "behind-b")
	echoCheck(t, first, "through-the-proxy")

	if !sup.Running("bastion") {
		t.Error("the proxy host should have been started with the connections through it")
	}
	if got, want := proxySrv.connCount(), 1; got != want {
		t.Errorf("the proxy host accepted %v connections, want %v: they should share one", got, want)
	}

	// The proxy host carries the other connection still, so it stays.
	if err := sup.Stop("behind-a"); err != nil {
		t.Fatal(err)
	}
	if !sup.Running("bastion") {
		t.Error("the proxy host is still carrying behind-b")
	}
	echoCheck(t, second, "still-through-the-proxy")

	// With nothing left to carry it stops too, since it has no forwards of
	// its own and was started only for these.
	if err := sup.Stop("behind-b"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for sup.Running("bastion") {
		if time.Now().After(deadline) {
			t.Fatal("the proxy host was not stopped with the last connection through it")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSupervisorPortInUse covers a connection whose local port something else
// has already bound, which is reported when it is asked for rather than
// asynchronously once it has failed.
func TestSupervisorPortInUse(t *testing.T) {
	userPriv, userSigner := newTestKey(t)
	_, hostSigner := newTestKey(t)
	serveTestAgent(t, userPriv)
	srv := newTestSSHServer(t, hostSigner, userSigner, "testuser")
	echo := echoServer(t)
	shared := freePort(t)

	sup, _ := newTestSupervisor(t, liveConfig(t, srv, knownHostsLine(srv, hostSigner.PublicKey()), fmt.Sprintf(`
connections:
  - name: one
    host: $ADDR
    ssh:
      user: testuser
      known_hosts: [$KNOWN]
      port_forward:
        groups: [a]
        forwards: [%d:%d]
  - name: two
    host: $ADDR
    ssh:
      user: testuser
      known_hosts: [$KNOWN]
      port_forward:
        groups: [b]
        forwards: [%d:%d]
`, shared, echo, shared, echo)))

	if err := sup.Start("one"); err != nil {
		t.Fatal(err)
	}
	err := sup.Start("two")
	if err == nil || !strings.Contains(err.Error(), "is already forwarded by \"one\"") {
		t.Errorf("got %v, want an error naming the connection that has the port", err)
	}
	if sup.Running("two") {
		t.Error("the connection that could not be started should not be running")
	}
}

// TestSupervisorResolve covers what a name from the page selects, and what it
// is told when the name selects nothing it can start.
func TestSupervisorResolve(t *testing.T) {
	sup, _ := newTestSupervisor(t, `
connections:
  - name: bastion
    host: bastion.example.com
    type: ssh
  - name: db
    host: db.internal
    ssh:
      proxy: bastion
      port_forward:
        groups: [ci]
        forwards: [5432:5432]
  - name: build-box
    host: build.example.com
    ssh:
      tmux:
        command: tmux -CC new -A -s main
`)
	for _, tc := range []struct {
		target string
		want   []string
		err    string
	}{
		{target: "ci", want: []string{"db"}},
		{target: "all", want: []string{"db"}},
		{target: "db", want: []string{"db"}},
		// A proxy host is started with whatever is reached through it, and a
		// connection that only runs tmux is not for this command at all.
		{target: "bastion", err: "is the proxy host of other connections"},
		{target: "build-box", err: "it is for 'pconn tmux'"},
		{target: "nosuch", err: "no group or connection named"},
	} {
		conns, err := sup.resolve(tc.target)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%v: got %v, want an error containing %q", tc.target, err, tc.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v: %v", tc.target, err)
			continue
		}
		var names []string
		for _, c := range conns {
			names = append(names, c.Name)
		}
		if strings.Join(names, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%v: got %v, want %v", tc.target, names, tc.want)
		}
	}
}

// TestSupervisorPage covers the page's view of a configuration in which
// nothing has been started: everything configured is listed, which is what
// makes it a list of what is available rather than of what is running.
func TestSupervisorPage(t *testing.T) {
	sup, _ := newTestSupervisor(t, `
connections:
  - name: web
    host: web.example.com
    ssh:
      port_forward:
        groups: [ci, dev]
        forwards: [8080:80]
  - name: db
    host: db.example.com
    ssh:
      port_forward:
        forwards:
          - forward: 5432:5432
            url: none
`)
	data := sup.page()
	if got, want := len(data.Groups), 2; got != want {
		t.Fatalf("got %v groups, want %v", got, want)
	}
	if got, want := data.Groups[0].Name, "ci"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if got, want := data.Groups[0].URLs, []string{"http://127.0.0.1:8080"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("got %v, want %v", got, want)
	}
	// web belongs to two groups, so it is shown under both, but the count is
	// of connections and not of rows.
	if got, want := data.Total, 2; got != want {
		t.Errorf("got %v connections, want %v", got, want)
	}
	if got, want := data.Up, 0; got != want {
		t.Errorf("got %v up, want %v", got, want)
	}
	// A forward that is not a web service has no URL to open, so the group it
	// is in has nothing to open for it.
	if len(data.Ungrouped) != 1 || len(data.Ungrouped[0].URLs) != 0 {
		t.Errorf("got %+v, want db with no URLs", data.Ungrouped)
	}
	if got, want := data.Ungrouped[0].Status, "stopped"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}
