// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubwebhook

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// unsignalledCapacity is a Capacity that has no change signal at all, which
// WithCapacity allows: such an implementation relies on the listener polling.
type unsignalledCapacity struct {
	available atomic.Bool
}

func (c *unsignalledCapacity) HasCapacity() bool { return c.available.Load() }

func (c *unsignalledCapacity) Changed() <-chan struct{} { return nil }

// TestWaitForCapacityPolls covers the safety net: capacity that becomes
// available without being signalled is still noticed, rather than leaving the
// listener waiting for a signal that never arrives.
func TestWaitForCapacityPolls(t *testing.T) {
	capacity := &unsignalledCapacity{}
	l := New("http://relay.invalid", nil, WithCapacity(capacity))
	l.pollInterval = time.Millisecond

	done := make(chan bool, 1)
	go func() { done <- l.waitForCapacity(context.Background()) }()

	select {
	case got := <-done:
		t.Fatalf("waitForCapacity returned %v before there was any capacity", got)
	case <-time.After(50 * time.Millisecond):
	}

	capacity.available.Store(true)
	select {
	case got := <-done:
		if !got {
			t.Error("waitForCapacity reported that it did not wait for capacity")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the capacity poll to notice")
	}
}

// TestWaitForCapacityStopped covers the listener being stopped, or its context
// cancelled, whilst it is waiting for capacity: Listen returns rather than
// waiting for capacity it no longer has any use for.
func TestWaitForCapacityStopped(t *testing.T) {
	capacity := &unsignalledCapacity{}

	stopped := New("http://relay.invalid", nil, WithCapacity(capacity))
	stopped.Stop()
	if stopped.waitForCapacity(context.Background()) {
		t.Error("waitForCapacity reported capacity for a stopped listener")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if New("http://relay.invalid", nil, WithCapacity(capacity)).waitForCapacity(ctx) {
		t.Error("waitForCapacity reported capacity for a cancelled context")
	}
}
