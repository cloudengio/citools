// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"cloudeng.io/sync/synctestutil"
	basepool "cloudeng.io/vms/vmspool"
)

const outcomeSuccess = "success"

// completedVM is the finished work a test parks: the VM it ran on and nothing
// else, since the queue reports on the VM rather than the work.
type completedVM struct {
	vm *basepool.VM
}

func (c completedVM) GetVM() *basepool.VM                   { return c.vm }
func (c completedVM) GetLogger(l *slog.Logger) *slog.Logger { return l }

// withSuccessOutcome configures a set with one outcome that holds its VMs for
// longer than any test runs, so that nothing expires while a test looks at it.
func withSuccessOutcome() Option {
	return WithCompletionOutcomes(
		basepool.WithCompletionOutcome(outcomeSuccess, 10, time.Hour))
}

// TestPoolCompletionQueue covers the queue a set creates for its pools: the
// set reports it, so does each pool, so does every VM a pool hands out, the
// pool counts the VMs it is holding, and closing the set closes the queue so
// that those VMs are released.
func TestPoolCompletionQueue(t *testing.T) {
	ctx := context.Background()
	pool, pools := soloPool(ctx, t, "parked", mockPool(2, MockConfig{}),
		withSuccessOutcome())
	q := pools.CompletionQueue()
	if q == nil {
		t.Fatal("the set created no queue for the outcome it was configured with")
	}
	if got := pool.CompletionQueue(); got != q {
		t.Errorf("the pool reports %v as its queue, want the set's %v", got, q)
	}
	snapshotWithin(t, pools, "parked", 2)

	vm, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The VM carries the queue, which is how work that has finished with it
	// parks it without being handed the queue as well.
	cq := vm.CompletionQueue()
	if cq != q {
		t.Fatalf("the VM reports %v as its queue, want its pool's %v", cq, q)
	}
	cq.Push(basepool.CompletionEvent{VM: completedVM{vm: vm}, Outcome: outcomeSuccess})

	// The pool reports the VM as completing rather than merely acquired,
	// which is what tells a user interface where it has got to.
	snap, err := pools.PoolStatus(ctx, "parked")
	if err != nil {
		t.Fatalf("PoolStatus: %v", err)
	}
	if got, want := snap.Completing, 1; got != want {
		t.Errorf("completing: got %v, want %v (vms %v)", got, want, len(snap.VMs))
	}

	// Closing the set closes the queue, after its pools, so the VM it was
	// holding is deleted rather than left behind.
	if err := pools.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}
	if got := q.VMIDs(); slices.Contains(got, vm.ID()) {
		t.Errorf("the queue still holds %v after the set was closed: %v", vm.ID(), got)
	}
}

// TestClosePoolKeepsCompletionQueue covers the ownership of the queue: it is
// the set's, so closing one of the set's pools neither closes it nor releases
// what it is holding for the pools that are still running.
func TestClosePoolKeepsCompletionQueue(t *testing.T) {
	ctx := context.Background()
	pools, err := NewPools(ctx, map[string]PoolConfig{
		"one": mockPool(1, MockConfig{}),
		"two": mockPool(1, MockConfig{}),
	}, discard, withSuccessOutcome())
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	q := pools.CompletionQueue()

	// A VM of pool two is parked, and then pool one is closed: the queue holds
	// VMs of either pool, so the one that was parked must survive it.
	vm, err := pools.Acquire(ctx, "two")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	q.Push(basepool.CompletionEvent{VM: completedVM{vm: vm}, Outcome: outcomeSuccess})
	if err := pools.ClosePool(ctx, "one"); err != nil {
		t.Fatalf("ClosePool: %v", err)
	}
	if q.Out(outcomeSuccess) == nil {
		t.Error("closing one pool of the set closed the set's queue")
	}
	if got := q.VMIDs(); !slices.Contains(got, vm.ID()) {
		t.Errorf("the queue no longer holds %v, parked from pool two, after pool one was closed: %v", vm.ID(), got)
	}

	// Closing the set is what releases it.
	if err := pools.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}
	if got := q.VMIDs(); slices.Contains(got, vm.ID()) {
		t.Errorf("the queue still holds %v after the set was closed: %v", vm.ID(), got)
	}
}

// TestCompletionQueueClosedWhenPoolsFail covers the queue of a set that then
// fails to start: there is no set for anything to close, so NewPools closes
// the queue it created rather than leaving its goroutines running.
func TestCompletionQueueClosedWhenPoolsFail(t *testing.T) {
	defer synctestutil.AssertNoGoroutinesRacy(t, 5*time.Second)()
	_, err := NewPools(context.Background(), map[string]PoolConfig{"nothing": {}},
		discard, withSuccessOutcome())
	if err == nil {
		t.Fatal("a set whose only pool has no backend was created")
	}
}

// TestPoolWithoutCompletionQueue covers a set configured with no outcomes,
// which is an application that deletes each VM as it finishes with it: no
// queue is created, so neither the set nor its pools nor their VMs have one to
// report, and nothing is ever counted as completing.
func TestPoolWithoutCompletionQueue(t *testing.T) {
	ctx := context.Background()
	pool, pools := soloPool(ctx, t, "unparked", mockPool(1, MockConfig{}))
	defer pools.Close(ctx) //nolint:errcheck // the test has already reported anything that matters.

	if got := pools.CompletionQueue(); got != nil {
		t.Errorf("the set reports %v as its queue, want nil", got)
	}
	if got := pool.CompletionQueue(); got != nil {
		t.Errorf("CompletionQueue: got %v, want nil", got)
	}
	snap := snapshotWithin(t, pools, "unparked", 1)
	if got, want := snap.Completing, 0; got != want {
		t.Errorf("completing: got %v, want %v", got, want)
	}

	vm, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := vm.CompletionQueue(); got != nil {
		t.Errorf("the VM reports %v as its queue, want nil", got)
	}
	if err := vm.Delete(ctx); err != nil {
		t.Errorf("Delete: %v", err)
	}
}
