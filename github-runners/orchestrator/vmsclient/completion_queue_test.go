// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"cloudeng.io/vms/vmspool"
)

// testPayload is a CompletionEventPayload carrying no VM, so that Close has
// nothing to delete and exercises only the drain.
type testPayload struct{}

func (testPayload) GetVM() *vmspool.VM                    { return nil }
func (testPayload) GetLogger(l *slog.Logger) *slog.Logger { return l }

// closeWithin runs Close in a goroutine and fails if it has not returned within
// the given time, so that a hang is reported rather than stalling the suite.
func closeWithin(t *testing.T, q *CompletionQueue[testPayload], within time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- q.Close(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(within):
		t.Fatalf("Close did not return within %v", within)
	}
}

// TestCompletionQueueClose covers the ordinary case: the queue's context is
// still live, so closing In makes the FIFO flush and close Out.
func TestCompletionQueueClose(t *testing.T) {
	q := NewCompletionQueue[testPayload](context.Background(), 10, time.Minute, time.Minute)
	q.PushSuccess(CompletionEvent[testPayload]{Payload: testPayload{}})
	q.PushFailure(CompletionEvent[testPayload]{Payload: testPayload{}}, nil)

	closeWithin(t, q, 30*time.Second)
}

// TestCompletionQueueCloseAfterCancel is the interrupt case. The FIFO is
// created with the run context, which is cancelled on ctrl-c; its goroutine
// then returns without closing Out, so a Close that ranged over Out would hang
// for ever rather than letting the orchestrator exit.
func TestCompletionQueueCloseAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	q := NewCompletionQueue[testPayload](ctx, 10, time.Minute, time.Minute)
	q.PushSuccess(CompletionEvent[testPayload]{Payload: testPayload{}})

	cancel()
	// Let the FIFO goroutines observe the cancellation and exit.
	time.Sleep(50 * time.Millisecond)

	closeWithin(t, q, drainTimeout*4)
}

// TestCompletionQueueCloseEmptyAfterCancel covers the same case with nothing
// queued, which is the common one: an idle orchestrator interrupted at rest.
func TestCompletionQueueCloseEmptyAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	q := NewCompletionQueue[testPayload](ctx, 10, time.Minute, time.Minute)
	cancel()
	time.Sleep(50 * time.Millisecond)

	closeWithin(t, q, drainTimeout*4)
}

type testPayloadVM struct {
	vm *vmspool.VM
}

func (p testPayloadVM) GetVM() *vmspool.VM                    { return p.vm }
func (p testPayloadVM) GetLogger(l *slog.Logger) *slog.Logger { return l }

func TestCompletionQueueVMTracking(t *testing.T) {
	ctx := context.Background()
	cfg := map[string]PoolConfig{
		"test": mockPool(1, MockConfig{}),
	}
	p := cfg["test"]
	p.StagingBehaviour = vmspool.StagingBehaviourRunning
	cfg["test"] = p
	pools, err := NewPools(ctx, cfg, discard)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pools.Close(ctx) }()

	vm, err := pools.Acquire(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}

	q := NewCompletionQueue[testPayloadVM](ctx, 10, time.Minute, time.Minute)
	defer func() { _ = q.Close(ctx) }()

	subCh, subCancel := q.Subscribe(ctx)
	defer subCancel()

	if q.ContainsVM(vm.ID()) {
		t.Errorf("queue should not contain VM before push")
	}

	q.PushSuccess(CompletionEvent[testPayloadVM]{
		Payload: testPayloadVM{vm: vm},
	})

	select {
	case <-subCh:
	case <-time.After(time.Second):
		t.Fatal("expected notification on push")
	}

	if !q.ContainsVM(vm.ID()) {
		t.Errorf("queue should contain VM %s after push", vm.ID())
	}
	vmIDs := q.VMIDs()
	if !vmIDs[vm.ID()] {
		t.Errorf("VMIDs should contain %s, got %v", vm.ID(), vmIDs)
	}

	q.Remove(vm.ID())
	if q.ContainsVM(vm.ID()) {
		t.Errorf("queue should not contain VM after Remove")
	}
}

