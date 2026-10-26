// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"testing"
	"time"

	"cloudeng.io/vms/vmspool"
)

// TestCapacityAny covers the rules that decide whether there is any work that
// can be taken on, which gates the orchestrator's connection to the webhook
// relay.
func TestCapacityAny(t *testing.T) {
	for _, tc := range []struct {
		name string
		cap  Capacity
		want bool
	}{
		// A VM that can be acquired now, or shortly because the pool is
		// replacing one, is capacity.
		{"available", Capacity{Pools: 1, Available: 1}, true},
		{"pending", Capacity{Pools: 1, Pending: 1}, true},
		{"both", Capacity{Pools: 2, Available: 1, Pending: 1}, true},
		// A pool whose VMs are all busy is not, and neither is one of several.
		{"busy", Capacity{Pools: 1}, false},
		{"all busy", Capacity{Pools: 3}, false},
		// With no pools there is nothing to gate on.
		{"no pools", Capacity{}, true},
	} {
		if got := tc.cap.Any(); got != tc.want {
			t.Errorf("%v: %+v: got %v, want %v", tc.name, tc.cap, got, tc.want)
		}
	}
}

// capacityWithin polls the pools until they report want capacity, which is not
// immediate: a pool fills itself in the background.
func capacityWithin(t *testing.T, pools *Pools, want bool) Capacity {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c := pools.Capacity()
		if c.Any() == want {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for capacity to be %v: got %+v", want, c)
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPoolsCapacity drives real pools through the mock backend to check that
// the capacity they report follows the VMs they have: a filled pool has it, and
// a pool whose every VM has been acquired does not.
func TestPoolsCapacity(t *testing.T) {
	ctx := context.Background()
	const size = 2
	pc := mockPool(size, MockConfig{Image: "fast-ci"})
	pc.StagingBehaviour = vmspool.StagingBehaviourRunning
	pc.CreateBackoff.InitialDelay = time.Millisecond
	pc.CreateBackoff.Steps = 5

	pools, err := NewPools(ctx, map[string]PoolConfig{"fast": pc}, discard)
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer func() {
		if err := pools.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	c := capacityWithin(t, pools, true)
	if got, want := c.Pools, 1; got != want {
		t.Errorf("pools: got %v, want %v", got, want)
	}

	// Every VM of the only pool is now held by a caller, so there is nothing
	// left to run a job on: a pool is of a fixed size, so the VMs that are
	// acquired are not capacity for anything else.
	for i := range size {
		vm, err := pools.Acquire(ctx, "fast")
		if err != nil {
			t.Fatalf("Acquire %v: %v", i, err)
		}
		defer vm.Delete(context.Background()) //nolint:errcheck // best effort cleanup.
	}

	if c := capacityWithin(t, pools, false); c.Available != 0 || c.Pending != 0 {
		t.Errorf("a fully acquired pool reports capacity: %+v", c)
	}
}
