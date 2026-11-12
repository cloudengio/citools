// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	basepool "cloudeng.io/vms/vmspool"
	"cloudeng.io/vms/vmstestutil"
)

// listingBackend lists the VMs it is given and fails the inspection of the
// ones named in cannotInspect, which is the case a snapshot has to survive: a
// VM that exists but cannot be read.
type listingBackend struct {
	failingBackend
	vms           []basepool.VMInfo
	cannotInspect map[string]bool
}

func (b listingBackend) List(context.Context) ([]basepool.VMInfo, error) {
	return b.vms, nil
}

func (b listingBackend) Get(_ context.Context, name string) (basepool.VMDetail, error) {
	if b.cannotInspect[name] {
		return basepool.VMDetail{}, errors.New("this VM cannot be inspected")
	}
	return basepool.VMDetail{VMInfo: basepool.VMInfo{Name: name}}, nil
}

// TestGetSnapshotVMDetailsUnavailable covers a snapshot of a pool one of whose
// VMs cannot be inspected: it is named as having failed and is in the snapshot
// with the backend's defaults standing in for it, rather than the whole
// snapshot being lost over one VM.
func TestGetSnapshotVMDetailsUnavailable(t *testing.T) {
	ctx := context.Background()
	backend := listingBackend{
		vms:           []basepool.VMInfo{{Name: "one"}, {Name: "two"}},
		cannotInspect: map[string]bool{"two": true},
	}
	pool := basepool.New(&basepool.CachingProvider{Provider: backend}, "flaky")
	defer pool.Close(ctx) //nolint:errcheck // nothing was started, so there is nothing to reclaim.

	snap, failed, err := GetSnapshot(ctx, pool)
	if err == nil {
		t.Error("the VM that could not be inspected was not reported as an error")
	}
	if got, want := failed, []string{"two"}; !slices.Equal(got, want) {
		t.Errorf("failed: got %v, want %v", got, want)
	}
	// The snapshot is sound: both VMs exist, so both are reported.
	if got, want := len(snap.VMs), 2; got != want {
		t.Errorf("got %v VMs, want %v: the uninspectable VM was dropped", got, want)
	}
	if got, want := snap.Name, "flaky"; got != want {
		t.Errorf("name: got %v, want %v", got, want)
	}
}

// TestGetSnapshotWithoutCachingProvider covers a pool whose backend is not the
// caching wrapper this package creates its pools with, and so cannot report the
// VMs that exist: there is no snapshot to be had, and the error says which pool
// and why.
func TestGetSnapshotWithoutCachingProvider(t *testing.T) {
	ctx := context.Background()
	pool := basepool.New(failingBackend{}, "bare")
	defer pool.Close(ctx) //nolint:errcheck // nothing was started, so there is nothing to reclaim.

	_, _, err := GetSnapshot(ctx, pool)
	if !errors.Is(err, ErrNocachingProvider) {
		t.Errorf("got %v, want ErrNocachingProvider", err)
	}
	if err == nil || !strings.Contains(err.Error(), "bare") {
		t.Errorf("the error does not name the pool: %v", err)
	}
}

// flakyMockConfig configures a mock pool whose VMs can be created but never
// inspected, so that a set has to report a pool none of whose VMs can be read.
type flakyMockConfig struct {
	MockConfig
}

func (c flakyMockConfig) Backend(pool string) basepool.ProviderNew {
	return flakyProviderNew{ProviderNew: c.MockConfig.Backend(pool)}
}

type flakyProviderNew struct {
	basepool.ProviderNew
}

func (f flakyProviderNew) New(ctx context.Context) (basepool.Provider, error) {
	b, err := f.ProviderNew.New(ctx)
	if err != nil {
		return nil, err
	}
	return flakyBackend{Provider: b}, nil
}

type flakyBackend struct {
	basepool.Provider
}

func (flakyBackend) Get(context.Context, string) (basepool.VMDetail, error) {
	return basepool.VMDetail{}, errors.New("this VM cannot be inspected")
}

// TestPoolStatusSurvivesUnreadableVMs covers what the set does with those
// per-VM failures: it reports the pool rather than the error, because a caller
// handed one shows nothing at all and the snapshot is worth more than that.
func TestPoolStatusSurvivesUnreadableVMs(t *testing.T) {
	ctx := context.Background()
	cfg := backendPool(2, flakyMockConfig{MockConfig: MockConfig{Image: "flaky-ci"}})
	_, pools := soloPool(ctx, t, "flaky", cfg)
	defer pools.Close(ctx) //nolint:errcheck // the test has already reported anything that matters.

	snap := snapshotWithin(t, pools, "flaky", 2)
	if got, want := len(snap.VMs), 2; got != want {
		t.Errorf("got %v VMs, want %v", got, want)
	}
	// Every VM is reported with the backend's defaults, since none of them
	// could be read.
	want := vmstestutil.DefaultMockResources
	for _, vm := range snap.VMs {
		if got := vm.VMResources; got != want {
			t.Errorf("%v: resources %+v, want %+v", vm.Name, got, want)
		}
	}
}

// TestPoolsCountersPerPool covers the counters a set accumulates through its
// shared tracker: each pool's snapshot reports its own events, and only its
// own, with no event channel or tracker per pool to keep them apart.
func TestPoolsCountersPerPool(t *testing.T) {
	ctx := context.Background()
	pools, err := NewPools(ctx, map[string]PoolConfig{
		"one": mockPool(1, MockConfig{}),
		"two": mockPool(1, MockConfig{}),
	}, discard)
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer pools.Close(ctx) //nolint:errcheck // the test has already reported anything that matters.

	vm, err := pools.Acquire(ctx, "one")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := vm.Delete(ctx); err != nil {
		t.Errorf("Delete: %v", err)
	}

	// Only pool one was acquired from, so only pool one counts it. The
	// counting is asynchronous, so the count is waited for rather than
	// assumed to have happened by now.
	acquired := basepool.EventAcquired.String()
	deadline := time.Now().Add(5 * time.Second)
	for {
		one, err := pools.PoolStatus(ctx, "one")
		if err != nil {
			t.Fatalf("PoolStatus: %v", err)
		}
		if one.Counters[acquired] == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pool one: %v = %d, want 1", acquired, one.Counters[acquired])
		}
		time.Sleep(10 * time.Millisecond)
	}
	two, err := pools.PoolStatus(ctx, "two")
	if err != nil {
		t.Fatalf("PoolStatus: %v", err)
	}
	if got := two.Counters[acquired]; got != 0 {
		t.Errorf("pool two: %v = %d, want 0: the VM was acquired from pool one", acquired, got)
	}
	// Pool two has still counted its own filling, so its counters are its own
	// rather than empty.
	if len(two.Counters) == 0 {
		t.Error("pool two counted none of its own events")
	}
	if two.Updated.IsZero() {
		t.Error("pool two recorded no event time of its own")
	}
}

// TestPoolsSubscribe covers the set's change signal, which covers every pool
// so that a user interface can watch all of them at once.
func TestPoolsSubscribe(t *testing.T) {
	ctx := context.Background()
	pools, err := NewPools(ctx, map[string]PoolConfig{
		"one": mockPool(1, MockConfig{}),
		"two": mockPool(1, MockConfig{}),
	}, discard)
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer pools.Close(ctx) //nolint:errcheck // the test has already reported anything that matters.

	ch, release := pools.Subscribe(ctx)

	// Acquiring a VM is a change to one of the pools, which reaches the
	// merged channel whichever pool it was.
	vm, err := pools.Acquire(ctx, "one")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer vm.Delete(context.Background()) //nolint:errcheck // best effort cleanup.
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no change signal after a VM was acquired")
	}

	// Releasing the subscription closes the merged channel, so a reader sees
	// the subscription end rather than blocking for ever.
	release()
	select {
	case _, ok := <-ch:
		if ok {
			// A signal published before the release may still be buffered;
			// the next receive must be the close.
			if _, ok := <-ch; ok {
				t.Error("the merged channel was not closed by the release")
			}
		}
	case <-time.After(5 * time.Second):
		t.Error("the merged channel was not closed by the release")
	}
}

// TestPoolsSubscribeReleasedTwice covers a caller that releases a
// subscription more than once, which a merged channel must survive: closing
// it twice would panic.
func TestPoolsSubscribeReleasedTwice(t *testing.T) {
	ctx := context.Background()
	pools, err := NewPools(ctx, map[string]PoolConfig{"one": mockPool(1, MockConfig{})},
		discard)
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer pools.Close(ctx) //nolint:errcheck // the test has already reported anything that matters.

	_, release := pools.Subscribe(ctx)
	release()
	release()
}
