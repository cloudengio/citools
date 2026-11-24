// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// webTestConfig has a connection in two groups, one in none, a connection
// reached through a proxy host, a forward to a host that the remote host
// reaches, and a connection that only runs tmux.
const webTestConfig = `
connections:
  - name: bastion
    host: bastion.example.com
    type: ssh
  - name: dev-web
    host: web.dev.example.com
    ssh:
      user: admin
      port_forward:
        groups: [dev, frontend]
        forwards: [8080:80, 8443:443]
  - name: db
    host: db.internal
    ssh:
      proxy: bastion
      port_forward:
        groups: [dev]
        forwards: [5432:pg.internal:5432]
  - name: cache
    host: cache.internal
    ssh:
      port_forward:
        forwards: [6379:6379]
  - name: build-box
    host: build.example.com
    ssh:
      tmux:
        command: tmux -CC new -A -s main
`

// newWebServer returns a server for the connections of spec, without
// connecting any of them, along with the runners so that a test can put them
// into the states the page reports.
func newWebServer(t *testing.T, spec string) (*webServer, map[string]*connRunner) {
	t.Helper()
	cfg := parseConfig(t, spec)
	logger := slog.New(slog.DiscardHandler)
	reporter := newStatusReporter(logger, 0)
	cf := &ConnectFlags{UnlimitedRetries: true}

	runners := []*connRunner{}
	byName := map[string]*connRunner{}
	for _, conn := range cfg.Connections {
		var proxy *connRunner
		if hop, ok := conn.ProxyHost(); ok {
			proxy = byName[hop.Name]
		}
		r := newConnRunner(t.Context(), reporter, logger, conn, proxy, cf.connOpts())
		t.Cleanup(r.client.Close)
		runners = append(runners, r)
		byName[conn.Name] = r
	}
	return &webServer{src: fixedConns(runners), logger: logger}, byName
}

// get renders the page.
func get(t *testing.T, s *webServer, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handle(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

// TestWebPage covers what the page lists: every group with its connections,
// the connections that belong to no group, and for each of them the local port
// to connect to and what answers on it.
func TestWebPage(t *testing.T) {
	s, _ := newWebServer(t, webTestConfig)
	code, body := get(t, s, "/")
	if code != http.StatusOK {
		t.Fatalf("got %v, want %v", code, http.StatusOK)
	}
	for _, want := range []string{
		// Each group is a heading of its own, and a connection in two groups
		// appears under both.
		">dev</span>", ">frontend</span>", ">no group</span>",
		// The connections, with who to connect as and where.
		"dev-web", "admin@web.dev.example.com:22",
		// The local ports, which are the point of the page, and the far end of
		// each forward: the remote host's own loopback, or the host named.
		"localhost:8080", "127.0.0.1:80",
		"localhost:8443", "127.0.0.1:443",
		"localhost:5432", "pg.internal:5432",
		"localhost:6379",
		// A connection reached through a proxy host says so, and one that only
		// runs tmux has no port to report.
		"via bastion", "tmux session",
		// Nothing has been connected, so every connection is starting.
		`class="status s-starting"`,
		"0 of 5 established",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not contain %q", want)
		}
	}
	// The page loads nothing from anywhere: no script, stylesheet or image of
	// its own, so that it works with no network beyond this host.
	for _, unwanted := range []string{"src=", "<link "} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the page loads something from outside itself: %q", unwanted)
		}
	}
}

// TestWebPageState covers the state that the page reports for each connection,
// which is what says whether the port it names can be used yet.
func TestWebPageState(t *testing.T) {
	s, byName := newWebServer(t, webTestConfig)

	byName["dev-web"].waiter.established("a-key")
	byName["db"].waiter.connecting()
	byName["db"].waiter.attemptFailed(errors.New("no route to host"), true)

	code, body := get(t, s, "/")
	if code != http.StatusOK {
		t.Fatalf("got %v, want %v", code, http.StatusOK)
	}
	for _, want := range []string{
		`class="status s-up"`,
		`class="status s-connecting"`,
		// Why a connection cannot be established is what says whether to wait
		// or to go and fix something.
		"no route to host",
		"1 failed attempts",
		"1 of 5 established",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not contain %q:\n%v", want, body)
		}
	}

	// A connection that has been established and is being waited for again is
	// an outage rather than a start.
	byName["dev-web"].waiter.connecting()
	if _, body := get(t, s, "/"); !strings.Contains(body, `class="status s-reconnecting"`) {
		t.Error("a connection that was established and is waiting again is reconnecting")
	}
}

func TestWebPageEmpty(t *testing.T) {
	s := &webServer{src: fixedConns(nil), logger: slog.New(slog.DiscardHandler)}
	if _, body := get(t, s, "/"); !strings.Contains(body, "No connections are configured") {
		t.Errorf("got %v, want the page to say that there are none", body)
	}
}

func TestWebNotFound(t *testing.T) {
	s, _ := newWebServer(t, webTestConfig)
	if code, _ := get(t, s, "/elsewhere"); code != http.StatusNotFound {
		t.Errorf("got %v, want %v", code, http.StatusNotFound)
	}
}

func TestWebAddr(t *testing.T) {
	for _, tc := range []struct{ addr, want string }{
		// A bare port is served to this host only: the page names internal
		// hosts and the ports that reach them.
		{"8099", "127.0.0.1:8099"},
		// An address names the interface to serve on, which is how to ask for
		// one that is not the loopback.
		{"localhost:8099", "localhost:8099"},
		{"0.0.0.0:8099", "0.0.0.0:8099"},
		{":8099", ":8099"},
	} {
		if got := webAddr(tc.addr); got != tc.want {
			t.Errorf("%v: got %v, want %v", tc.addr, got, tc.want)
		}
	}
}

// TestWebServes covers the server end to end, and that an address which cannot
// be bound is reported rather than leaving the page silently unserved.
func TestWebServes(t *testing.T) {
	s, _ := newWebServer(t, webTestConfig)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if _, err := listenWeb(l.Addr().String(), s.src, s.logger); err == nil {
		t.Error("expected an address that is already in use to be reported")
	}

	srv, err := listenWeb("127.0.0.1:0", s.src, s.logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan struct{})
	go func() {
		defer close(served)
		srv.serve(ctx)
	}()

	resp, err := http.Get(srv.url()) //nolint:noctx // The server is this test's own.
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if !strings.Contains(string(body), "localhost:8080") {
		t.Errorf("the page does not list the forwards:\n%v", string(body))
	}

	// Cancelling is what stops it, and the port is released.
	cancel()
	<-served
	closed, err := net.Listen("tcp", srv.listener.Addr().String())
	if err != nil {
		t.Errorf("the address was not released: %v", err)
	} else {
		_ = closed.Close()
	}
}
