// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultStatusInterval is how often connections that have not yet been
// established are reported on by default.
const DefaultStatusInterval = 15 * time.Second

// connWaiter tracks whether one connection has been established and, whilst
// it has not, how long it has been waited for.
//
// It exists because a connection that is being retried is otherwise silent:
// the error from an attempt that will be retried is not returned by pssh, as
// the connection is still being maintained. The waiter is registered with
// pssh.WithOnAttemptFailed to report each of those errors as it happens, and
// repeats the most recent one with every status report, so that a host behind
// a dropped VPN or a firewall is distinguishable from pconn having hung.
type connWaiter struct {
	logger    *slog.Logger // carries the connection's identifying attributes
	conn      ConnectionConfig
	unlimited bool

	mu             sync.Mutex
	waitingSince   time.Time // zero once the connection has been established
	reported       bool      // the current wait has been reported as pending
	establishments int       // times the connection has been established
	attempts       int       // failed attempts since the wait began
	lastErr        error     // why the most recent attempt failed
}

// connecting records that the wait for a connection has begun.
func (w *connWaiter) connecting() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.waitingSince = time.Now()
	w.reported = false
	w.attempts = 0
	w.lastErr = nil
}

// attemptFailed reports a failed attempt to establish the connection, and is
// registered with pssh.WithOnAttemptFailed. Every attempt is logged, and not
// just the most recent one carried by the next status report, because the
// errors differ: a connection that is refused on one attempt and times out on
// the next is a different problem from one that is refused every time, and
// the error carries the address that was actually dialled.
//
// retryable says whether the error is one that retrying can resolve. It is
// logged because it is the difference between a connection that will come up
// on its own and one that needs something fixed.
func (w *connWaiter) attemptFailed(err error, retryable bool) {
	w.mu.Lock()
	w.attempts++
	attempt := w.attempts
	w.lastErr = err
	w.mu.Unlock()

	// Logged outside the lock: it is the status reporter's lock too.
	w.logger.Warn("connection attempt failed",
		"attempt", attempt,
		"err", err,
		"retryable", retryable,
	)
}

// established records, and reports, that the connection has been established.
// key identifies the key it was authenticated with, as per
// pssh.Client.AuthKey.
//
// A wait that was reported as pending is reported as over at the same level,
// so that the resolution is as visible as the complaint was, and a second or
// subsequent establishment is reported as a reconnection, which is what makes
// a connection that keeps dropping visible. Note that the drop itself cannot
// be reported here: pssh says when it has established a connection, but not
// when one it is maintaining has been lost.
func (w *connWaiter) established(key string) {
	w.mu.Lock()
	w.establishments++
	establishments := w.establishments
	wasReported := w.reported
	attempts, lastErr := w.attempts, w.lastErr
	var waited time.Duration
	if !w.waitingSince.IsZero() {
		waited = time.Since(w.waitingSince)
	}
	w.waitingSince = time.Time{}
	w.reported = false
	w.attempts = 0
	w.lastErr = nil
	w.mu.Unlock()

	// The logger already identifies the connection and its address, so
	// repeating them here would only duplicate the attributes.
	attrs := []any{"key", key}
	if waited > 0 {
		// A reconnection has no wait to report: it is not known when the
		// connection that it replaces was lost.
		attrs = append(attrs, "waited", waited.Round(time.Millisecond))
	}
	if attempts > 0 {
		// What it took to get here, which for a reconnection is the only
		// account of the outage there is.
		attrs = append(attrs, "failed_attempts", attempts, "last_error", lastErr)
	}
	switch {
	case establishments > 1:
		w.logger.Warn("connection re-established", append(attrs, "establishments", establishments)...)
	case wasReported:
		w.logger.Warn("connection established", attrs...)
	default:
		w.logger.Info("connection established", attrs...)
	}
}

// pending reports whether the connection is still waiting to be established
// and, if it is, how long it has been waiting, marking the wait as reported.
func (w *connWaiter) pending() (waited time.Duration, attempts int, lastErr error, pending bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.waitingSince.IsZero() {
		return 0, 0, nil, false
	}
	w.reported = true
	return time.Since(w.waitingSince), w.attempts, w.lastErr, true
}

// report logs what is known about the connection whilst it has not been
// established, and reports whether it logged anything. It says how long the
// connection has been waited for, what it is waiting on, and whether it will
// keep trying, since none of that can be inferred from silence.
func (w *connWaiter) report() bool {
	waited, attempts, lastErr, pending := w.pending()
	if !pending {
		return false
	}
	attrs := []any{
		"user", w.conn.ParsedUser(),
		"waiting_for", waited.Round(time.Millisecond),
		"dial_timeout", w.conn.DialTimeout,
		"unlimited_retries", w.unlimited,
		"forwards", formatForwards(w.conn.Forwards),
		"failed_attempts", attempts,
	}
	if lastErr != nil {
		// Why the last attempt failed, which is what says whether this is
		// something to wait out or something to go and fix.
		attrs = append(attrs, "last_error", lastErr)
	}
	w.logger.Warn("connection not yet established", attrs...)
	return true
}

// statusReporter periodically reports on the connections that have not yet
// been established. Connections are registered with add before run is
// called, which is the only ordering it requires: the waiters themselves are
// safe for concurrent use.
type statusReporter struct {
	logger   *slog.Logger
	interval time.Duration
	waiters  []*connWaiter
}

func newStatusReporter(logger *slog.Logger, interval time.Duration) *statusReporter {
	return &statusReporter{logger: logger, interval: interval}
}

// add registers conn with the reporter and returns the waiter that tracks it.
// logger should already carry the connection's identifying attributes.
func (r *statusReporter) add(logger *slog.Logger, conn ConnectionConfig, unlimited bool) *connWaiter {
	w := &connWaiter{logger: logger, conn: conn, unlimited: unlimited}
	r.waiters = append(r.waiters, w)
	return w
}

// report logs the state of every connection that has not yet been
// established, plus a summary when more than one of them is outstanding, and
// returns how many are. Reporting each connection separately is what carries
// the detail; the summary is what makes the scale of an outage obvious.
func (r *statusReporter) report() int {
	pending := 0
	for _, w := range r.waiters {
		if w.report() {
			pending++
		}
	}
	if pending > 1 {
		r.logger.Warn("connections not yet established",
			"pending", pending,
			"connections", len(r.waiters),
		)
	}
	return pending
}

// run reports on the connections every interval until ctx is cancelled. An
// interval of zero or less disables reporting, and run returns immediately.
func (r *statusReporter) run(ctx context.Context) {
	if r.interval <= 0 {
		return
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.report()
		}
	}
}
