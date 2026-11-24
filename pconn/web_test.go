// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
	s.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
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

// TestForwardsLocalPort covers the lookup that anything wanting to bind a
// local port asks first, which is what keeps the page's port and a forward's
// port apart.
func TestForwardsLocalPort(t *testing.T) {
	cfg := parseConfig(t, `
connections:
  - name: web
    host: web.example.com
    ssh:
      port_forward:
        forwards: [8080:80, 8443:443]
  - name: db
    host: db.example.com
    ssh:
      port_forward:
        forwards: [5432:pg.internal:5432]
  # A connection that only runs tmux forwards nothing.
  - name: build-box
    host: build.example.com
    ssh:
      tmux:
        command: tmux -CC new -A -s main
`)
	for _, tc := range []struct {
		port int
		want string
	}{
		{8080, "web"},
		{8443, "web"},
		// The local port is what is bound here; the remote port is not.
		{5432, "db"},
		{80, ""},
		{443, ""},
		{8099, ""},
	} {
		name, ok := cfg.ForwardsLocalPort(tc.port)
		if got := name; got != tc.want || ok != (tc.want != "") {
			t.Errorf("port %v: got %q, want %q", tc.port, got, tc.want)
		}
	}
}

// TestWebPortClash covers the check that keeps the page off a port that a
// connection forwards: both bind it here, so one of them would fail.
func TestWebPortClash(t *testing.T) {
	forwarded := freePort(t)
	cfg := parseConfig(t, fmt.Sprintf(`
connections:
  - name: web
    host: web.example.com
    ssh:
      port_forward:
        forwards: [%d:80]
`, forwarded))

	logger := slog.New(slog.DiscardHandler)
	clashing, err := listenWeb(strconv.Itoa(forwarded), fixedConns(nil), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer clashing.close()
	err = clashing.checkForwardPorts(cfg)
	if err == nil || !strings.Contains(err.Error(), `connection "web" forwards that port`) {
		t.Errorf("got %v, want an error naming the connection that forwards the port", err)
	}

	// Another port is not a clash, and nor is asking for any port at all: the
	// one that is bound is what is checked, so a port of 0 is checked as the
	// port it was given.
	for _, addr := range []string{"127.0.0.1:0", strconv.Itoa(freePort(t))} {
		srv, err := listenWeb(addr, fixedConns(nil), logger)
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.checkForwardPorts(cfg); err != nil {
			t.Errorf("%v: %v", addr, err)
		}
		if got := srv.port(); got == 0 {
			t.Errorf("%v: the bound port should be known", addr)
		}
		srv.close()
	}
}

// TestWebBrowseURL covers the url that is opened in a browser, which is where
// the page is served except when it is served on every interface: a browser
// cannot use an unspecified address.
func TestWebBrowseURL(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	for _, tc := range []struct{ addr, want string }{
		{"127.0.0.1:0", "http://127.0.0.1:%d/"},
		// Served on every interface, so this host stands for itself.
		{"0.0.0.0:0", "http://127.0.0.1:%d/"},
		{":0", "http://127.0.0.1:%d/"},
	} {
		srv, err := listenWeb(tc.addr, fixedConns(nil), logger)
		if err != nil {
			t.Fatalf("%v: %v", tc.addr, err)
		}
		if got, want := srv.browseURL(), fmt.Sprintf(tc.want, srv.port()); got != want {
			t.Errorf("%v: got %v, want %v", tc.addr, got, want)
		}
		srv.close()
	}
}

// TestWebLogFile covers the link to the log: it is served by the page's own
// server, since a browser will not follow a file:// link from a page it was
// served over http, and it says so when there is no file to link to.
func TestWebLogFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "pconn.log")
	const logLine = "time=2026-11-24T09:00:00.000-08:00 level=INFO msg=\"connection established\""
	if err := os.WriteFile(logPath, []byte(logLine+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	s, _ := newWebServer(t, webTestConfig)
	s.logFile = logPath

	_, body := get(t, s, "/")
	for _, want := range []string{
		`href="/log"`,
		// The name is the link, the path is what it says on hover: the path
		// is what a person needs in order to tail it themselves.
		">pconn.log</a>",
		`title="` + logPath + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not contain %q:\n%v", want, body)
		}
	}

	// The log view shows the log and follows the end of it, so that it says
	// what happens next and not only what had happened when it was opened.
	code, view := get(t, s, "/log")
	if code != http.StatusOK {
		t.Fatalf("got %v: %v", code, view)
	}
	for _, want := range []string{
		"<title>pconn log</title>", ">pconn.log</h1>", logPath,
		`href="/log/raw"`, `data-raw="/log/raw"`, "the last 256KB", "setInterval(refresh",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the log view does not contain %q", want)
		}
	}

	// Its text is served separately, to be read in the browser rather than
	// downloaded.
	rec := httptest.NewRecorder()
	s.handleLogRaw(rec, httptest.NewRequest(http.MethodGet, webLogRawPath, nil))
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), "text/plain; charset=utf-8"; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if !strings.Contains(rec.Body.String(), logLine) {
		t.Errorf("got %q, want the contents of the log", rec.Body.String())
	}

	// The log is linked to rather than opened: nothing on the page opens it.
	if strings.Contains(body, "pconn-log") || strings.Contains(body, "openLog") {
		t.Error("the page opens the log by itself")
	}

	// With the logs going to the terminal there is nothing to link to, and
	// the page says that rather than offering a link that cannot work.
	s.logFile = ""
	_, body = get(t, s, "/")
	if strings.Contains(body, `href="/log"`) {
		t.Error("the page links to a log file that there is none of")
	}
	if !strings.Contains(body, "logging to the terminal") {
		t.Errorf("the page does not say where the logs go:\n%v", body)
	}
	for _, path := range []string{"/log", webLogRawPath} {
		code, body := get(t, s, path)
		if code != http.StatusNotFound {
			t.Errorf("%v: got %v, want %v", path, code, http.StatusNotFound)
		}
		if !strings.Contains(body, "logging to the terminal") {
			t.Errorf("%v: got %q, want an answer that says how to configure a log file", path, body)
		}
	}
}

// TestWebLogTail covers the end of a log that is larger than the page fetches:
// a log of any size is bounded to the last part of it, which starts at a line
// boundary rather than part way through one.
func TestWebLogTail(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "big.log")
	var buf bytes.Buffer
	for i := 0; buf.Len() <= webLogTail*2; i++ {
		fmt.Fprintf(&buf, "line %d: %s\n", i, strings.Repeat("x", 80))
	}
	if err := os.WriteFile(logPath, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}

	var tail bytes.Buffer
	if err := writeLogTail(&tail, logPath); err != nil {
		t.Fatal(err)
	}
	if tail.Len() > webLogTail {
		t.Errorf("got %v bytes, want no more than %v", tail.Len(), webLogTail)
	}
	if !strings.HasPrefix(tail.String(), "line ") {
		t.Errorf("the tail starts part way through a line: %q", tail.String()[:40])
	}
	// The end of the log is what it is for.
	if !strings.HasSuffix(tail.String(), buf.String()[buf.Len()-200:]) {
		t.Error("the tail does not end with the end of the log")
	}

	// A log smaller than the limit is served whole.
	small := filepath.Join(t.TempDir(), "small.log")
	if err := os.WriteFile(small, []byte("one\ntwo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tail.Reset()
	if err := writeLogTail(&tail, small); err != nil {
		t.Fatal(err)
	}
	if got, want := tail.String(), "one\ntwo\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// A log that cannot be read is reported rather than served as empty.
	if err := writeLogTail(&tail, filepath.Join(t.TempDir(), "nosuch.log")); err == nil {
		t.Error("expected a missing log to be reported")
	}
}
