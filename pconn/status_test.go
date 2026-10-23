// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer that may be written to by a logger whilst the
// test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newTestLogger returns a logger that records everything written to it, at
// every level, along with the buffer holding it.
func newTestLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, buf
}

// testWaiterConfig is a connection whose attributes the diagnostics report.
func testWaiterConfig(t *testing.T) ConnectionConfig {
	t.Helper()
	conn := ConnectionConfig{
		Name:        "pending-host",
		Host:        "pending.example.com",
		User:        "someone",
		DialTimeout: 7 * time.Second,
		Forwards:    []ForwardConfig{{LocalPort: 8080, RemotePort: 80}},
	}
	if err := conn.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return conn
}

// testConnLogger returns the logger that maintainConnections gives a waiter,
// ie. one that already carries the connection's identifying attributes, which
// the waiter relies on rather than repeating them in every line.
func testConnLogger(logger *slog.Logger, conn ConnectionConfig) *slog.Logger {
	return logger.With("connection", conn.Name, "type", conn.Type, "ssh.addr", conn.HostPort())
}

func newTestWaiter(t *testing.T, unlimited bool) (*connWaiter, *syncBuffer) {
	t.Helper()
	logger, buf := newTestLogger()
	r := newStatusReporter(logger, DefaultStatusInterval)
	conn := testWaiterConfig(t)
	return r.add(testConnLogger(logger, conn), conn, unlimited), buf
}

// assertLogged checks that every one of want appears in the log, and that
// none of reject does.
func assertLogged(t *testing.T, buf *syncBuffer, want []string, reject []string) {
	t.Helper()
	logged := buf.String()
	for _, w := range want {
		if !strings.Contains(logged, w) {
			t.Errorf("log does not contain %q:\n%s", w, logged)
		}
	}
	for _, r := range reject {
		if strings.Contains(logged, r) {
			t.Errorf("log unexpectedly contains %q:\n%s", r, logged)
		}
	}
}

func testConnWaiterQuietWhenPrompt(t *testing.T) {
	w, buf := newTestWaiter(t, true)
	w.connecting()

	// A connection that is established before anything has been reported
	// about it is not a diagnostic: it is logged at info, as before.
	w.established("test-key")
	assertLogged(t, buf,
		[]string{"level=INFO", "msg=\"connection established\"", "key=test-key", "waited="},
		[]string{"not yet established", "re-established", "level=WARN"})
}

func testConnWaiterReportsPending(t *testing.T) {
	w, buf := newTestWaiter(t, true)
	w.connecting()

	if !w.report() {
		t.Fatal("a connection that has not been established should be reported")
	}
	// The report has to say what is being waited for and whether waiting will
	// continue, since none of that can be inferred from silence. It is logged
	// at warn so that it is visible at the default logging level.
	assertLogged(t, buf, []string{
		"level=WARN",
		"msg=\"connection not yet established\"",
		"connection=pending-host",
		"ssh.addr=pending.example.com:22",
		"user=someone",
		"waiting_for=",
		"dial_timeout=7s",
		"unlimited_retries=true",
		"forwards=127.0.0.1:8080->:80",
		"failed_attempts=0",
	}, []string{"last_error"})

	// Having complained, the resolution is reported at the same level rather
	// than at info, where it would not be seen.
	w.established("test-key")
	assertLogged(t, buf,
		[]string{"level=WARN msg=\"connection established\"", "key=test-key"},
		[]string{"level=INFO"})
}

func testConnWaiterReportsReconnection(t *testing.T) {
	w, buf := newTestWaiter(t, false)
	w.connecting()
	w.established("test-key")
	// pssh re-establishes a dropped connection without saying so, which makes
	// a connection that keeps dropping invisible; each establishment after
	// the first is therefore reported as a reconnection, at warn.
	w.established("test-key")
	assertLogged(t, buf, []string{
		"level=WARN",
		"msg=\"connection re-established\"",
		"establishments=2",
	}, nil)

	// The drop cannot be timed, so no wait is claimed for a reconnection.
	reconnection := buf.String()[strings.Index(buf.String(), "re-established"):]
	if strings.Contains(reconnection, "waited=") {
		t.Errorf("a reconnection should not report a wait:\n%s", reconnection)
	}
}

func testConnWaiterNotPendingOnceEstablished(t *testing.T) {
	w, _ := newTestWaiter(t, true)
	w.connecting()
	w.established("test-key")
	if w.report() {
		t.Error("an established connection should not be reported as pending")
	}
	// Nor is one that was never attempted.
	fresh, _ := newTestWaiter(t, true)
	if fresh.report() {
		t.Error("a connection that has not been attempted should not be reported as pending")
	}
}

func testConnWaiterReportsWhyItFailed(t *testing.T) {
	w, buf := newTestWaiter(t, true)
	w.connecting()

	// Until an attempt has failed there is nothing to say about why, and a
	// reason is not invented.
	w.report()
	assertLogged(t, buf, []string{"failed_attempts=0"}, []string{"last_error"})

	// pssh does not return the error from an attempt it will retry, so
	// without the error it hands to attemptFailed nothing can say what is
	// wrong. Each attempt is reported as it fails, since the errors differ
	// and each carries the address that was dialled.
	w.attemptFailed(errors.New("connection refused"), true)
	w.attemptFailed(errors.New("no route to host"), true)
	assertLogged(t, buf, []string{
		"level=WARN msg=\"connection attempt failed\" connection=pending-host",
		"attempt=1 err=\"connection refused\" retryable=true",
		"attempt=2 err=\"no route to host\" retryable=true",
	}, nil)

	// A status report repeats the most recent of them, so that one line says
	// what the current state is.
	w.report()
	assertLogged(t, buf, []string{
		"failed_attempts=2",
		"last_error=\"no route to host\"",
	}, nil)
}

func testConnWaiterReportsUnretryableAttempt(t *testing.T) {
	w, buf := newTestWaiter(t, true)
	w.connecting()

	// An error that retrying cannot resolve is pssh's to return and the
	// command's to report, but saying which it was here is what distinguishes
	// waiting from needing to fix something.
	w.attemptFailed(errors.New("host key mismatch"), false)
	assertLogged(t, buf, []string{
		"msg=\"connection attempt failed\"",
		"err=\"host key mismatch\" retryable=false",
	}, nil)
}

func testConnWaiterReportsOutageCost(t *testing.T) {
	w, buf := newTestWaiter(t, true)
	w.connecting()
	w.attemptFailed(errors.New("connection refused"), true)
	w.attemptFailed(errors.New("connection refused"), true)

	// What it took to connect is also reported once it is connected, which for a
	// reconnection is the only account of the outage there is.
	w.established("test-key")
	assertLogged(t, buf, []string{
		"failed_attempts=2",
		"last_error=\"connection refused\"",
	}, nil)

	// The count starts again with the next wait rather than accumulating
	// across outages.
	w.connecting()
	w.report()
	assertLogged(t, buf, []string{"failed_attempts=0"}, nil)
}

func TestConnWaiter(t *testing.T) {
	t.Run("quiet when established promptly", testConnWaiterQuietWhenPrompt)
	t.Run("reports a pending connection", testConnWaiterReportsPending)
	t.Run("reports a reconnection", testConnWaiterReportsReconnection)
	t.Run("not pending once established", testConnWaiterNotPendingOnceEstablished)
	t.Run("reports why an attempt failed", testConnWaiterReportsWhyItFailed)
	t.Run("reports an unretryable attempt", testConnWaiterReportsUnretryableAttempt)
	t.Run("reports what an outage cost", testConnWaiterReportsOutageCost)
}

func testStatusReporterSummary(t *testing.T) {
	logger, buf := newTestLogger()
	r := newStatusReporter(logger, DefaultStatusInterval)
	conn := testWaiterConfig(t)
	first := r.add(testConnLogger(logger, conn), conn, true)
	second := r.add(testConnLogger(logger, conn), conn, true)

	// One outstanding connection needs no summary: its own line says it all.
	first.connecting()
	if got, want := r.report(), 1; got != want {
		t.Errorf("got %v pending, want %v", got, want)
	}
	assertLogged(t, buf, []string{"connection not yet established"}, []string{"connections not yet established"})

	// More than one does, so that the scale of an outage is obvious.
	second.connecting()
	if got, want := r.report(), 2; got != want {
		t.Errorf("got %v pending, want %v", got, want)
	}
	assertLogged(t, buf, []string{
		"msg=\"connections not yet established\"",
		"pending=2",
		"connections=2",
	}, nil)
}

func testStatusReporterNothingPending(t *testing.T) {
	logger, buf := newTestLogger()
	r := newStatusReporter(logger, DefaultStatusInterval)
	conn := testWaiterConfig(t)
	r.add(testConnLogger(logger, conn), conn, true).connecting()
	r.waiters[0].established("test-key")

	if got := r.report(); got != 0 {
		t.Errorf("got %v pending, want 0", got)
	}
	assertLogged(t, buf, nil, []string{"not yet established"})
}

func testStatusReporterRun(t *testing.T) {
	logger, buf := newTestLogger()
	r := newStatusReporter(logger, time.Millisecond)
	conn := testWaiterConfig(t)
	r.add(testConnLogger(logger, conn), conn, true).connecting()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		r.run(ctx)
		close(done)
	}()

	// The reporter keeps reporting for as long as it runs.
	for !strings.Contains(buf.String(), "connection not yet established") {
		select {
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for a report")
		default:
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the reporter to stop")
	}
}

func testStatusReporterDisabled(t *testing.T) {
	logger, buf := newTestLogger()
	for _, interval := range []time.Duration{0, -time.Second} {
		r := newStatusReporter(logger, interval)
		conn := testWaiterConfig(t)
		r.add(testConnLogger(logger, conn), conn, true).connecting()

		// An interval of zero or less disables reporting, so run must return
		// rather than leave a goroutine behind.
		done := make(chan struct{})
		go func() {
			r.run(t.Context())
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("interval %v: run did not return", interval)
		}
	}
	assertLogged(t, buf, nil, []string{"not yet established"})
}

func TestStatusReporter(t *testing.T) {
	t.Run("summarizes several pending connections", testStatusReporterSummary)
	t.Run("silent when nothing is pending", testStatusReporterNothingPending)
	t.Run("reports until cancelled", testStatusReporterRun)
	t.Run("disabled by a non-positive interval", testStatusReporterDisabled)
}
