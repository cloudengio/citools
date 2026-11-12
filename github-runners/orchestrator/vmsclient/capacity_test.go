// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"testing"
	"time"
)

// capacityWithin polls until the pools report want, which is not immediate: a
// pool fills itself in the background.
func capacityWithin(t *testing.T, pools *Pools, want bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if pools.HasCapacity() == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for capacity to be %v", want)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPoolsCapacity drives real pools through the mock backend to check that
// the capacity they report follows the VMs they have: a filled pool has it,
// and a pool whose every VM has been acquired does not.
func TestPoolsCapacity(t *testing.T) {
	ctx := context.Background()
	const size = 2
	pools, err := NewPools(ctx, map[string]PoolConfig{
		"fast": mockPool(size, MockConfig{Image: "fast-ci"}),
	}, discard)
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer func() {
		if err := pools.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	capacityWithin(t, pools, true)

	// Every VM of the only pool is now held by a caller, so there is nothing
	// left to run work on: a pool is of a fixed size, so the VMs that are
	// acquired are not capacity for anything else.
	for i := range size {
		vm, err := pools.Acquire(ctx, "fast")
		if err != nil {
			t.Fatalf("Acquire %v: %v", i, err)
		}
		defer vm.Delete(context.Background()) //nolint:errcheck // best effort cleanup.
	}
	capacityWithin(t, pools, false)
}

// TestNoPoolsCapacity covers a set with no pools in it, which has no capacity:
// work gated on it is left where it is rather than taken on and then failed
// for want of anything to run it.
func TestNoPoolsCapacity(t *testing.T) {
	ctx := context.Background()
	pools, err := NewPools(ctx, nil, discard)
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer func() {
		if err := pools.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	if pools.HasCapacity() {
		t.Error("a set with no pools reports capacity")
	}
	// A closed set has none either, which is what a shutdown leaves whilst
	// work is still being gated on it.
	if err := pools.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if pools.HasCapacity() {
		t.Error("a closed set reports capacity")
	}
}
