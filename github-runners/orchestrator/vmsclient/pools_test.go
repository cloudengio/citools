// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"cloudeng.io/sync/synctestutil"
	"cloudeng.io/vms"
	"cloudeng.io/vms/ociimage"
	basepool "cloudeng.io/vms/vmspool"
	"cloudeng.io/vms/vmstestutil"
)

// backendPool returns the configuration of a pool backed directly by backend
// and filling as quickly as it can, for the backends that no configuration
// file can select, see PoolConfig.Backend.
func backendPool(size int, backend BackendConfig) PoolConfig {
	pc := PoolConfig{Backend: backend}
	pc.Size = size
	pc.StagingBehaviour = basepool.StagingBehaviourRunning
	pc.CreateBackoff.InitialDelay = time.Millisecond
	pc.CreateBackoff.Steps = 5
	return pc
}

// snapshotWithin polls the named pool until it reports want VMs as available
// and the backend reports want VMs existing, since a pool fills itself in the
// background.
//
// Both counts are waited for because a snapshot is not taken at a single
// instant: it reads the backend's VMs before it reads the pool's own stats,
// so one taken whilst the pool is still filling can report a VM that was
// created in between as available without listing it. Gating on the stats
// alone would hand such a snapshot back and the caller would see fewer VMs
// than it waited for.
func snapshotWithin(t *testing.T, pools *Pools, name string, want int) PoolSnapshot {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, err := pools.PoolStatus(ctx, name)
		if err != nil {
			t.Fatalf("PoolStatus: %v", err)
		}
		if snap.Available >= want && len(snap.VMs) >= want {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %v available VMs: got %+v", want, snap)
			return snap
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// soloPool creates a set holding a single pool and returns both: the pool to
// exercise, and the set because that is what owns the accounting and so what
// has to be closed, see NewPools.
func soloPool(ctx context.Context, t *testing.T, name string, cfg PoolConfig, opts ...Option) (*basepool.Pool, *Pools) {
	t.Helper()
	pools, err := NewPools(ctx, map[string]PoolConfig{name: cfg}, discard, opts...)
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	pool, err := pools.Pool(name)
	if err != nil {
		t.Fatalf("Pool(%v): %v", name, err)
	}
	return pool, pools
}

// TestSinglePool covers a set holding one pool, which is how an application
// that wants a single pool has it: the pool reports its own identity and fills
// as any other does.
func TestSinglePool(t *testing.T) {
	ctx := context.Background()
	pool, pools := soloPool(ctx, t, "solo", mockPool(2, MockConfig{Image: "fast-ci"}))
	defer func() {
		if err := pools.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	if got, want := pools.Len(), 1; got != want {
		t.Errorf("pools: got %v, want %v", got, want)
	}

	if got, want := pool.Name(), "solo"; got != want {
		t.Errorf("name: got %v, want %v", got, want)
	}

	snap := snapshotWithin(t, pools, "solo", 2)
	if got, want := snap.Size, 2; got != want {
		t.Errorf("size: got %v, want %v", got, want)
	}
	if got, want := snap.Name, "solo"; got != want {
		t.Errorf("snapshot name: got %v, want %v", got, want)
	}
	// The pool's identity is read back from its backend, which is where a
	// snapshot gets it, see GetSnapshot.
	if got, want := snap.Kind, "mock"; got != want {
		t.Errorf("kind: got %v, want %v", got, want)
	}
	if got, want := snap.Image, "fast-ci"; got != want {
		t.Errorf("image: got %v, want %v", got, want)
	}
	if len(snap.VMs) != 2 {
		t.Errorf("got %v VMs, want 2", len(snap.VMs))
	}
	// The pool's own lifecycle events are counted, which is what a user
	// interface reports.
	if len(snap.Counters) == 0 {
		t.Error("no lifecycle events were counted")
	}
	if snap.Updated.IsZero() {
		t.Error("no event time was recorded")
	}

	// A mock VM reports no resources of its own, so the backend's defaults
	// stand in for them.
	want := vmstestutil.DefaultMockResources
	for _, vm := range snap.VMs {
		if got := vm.VMResources; got != want {
			t.Errorf("%v: resources %+v, want %+v", vm.Name, got, want)
		}
	}

	// A VM can be acquired and released, which is the whole point of a pool.
	vm, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if vm.ID() == "" {
		t.Error("the acquired VM has no ID")
	}
	if err := vm.Exec(ctx, io.Discard, io.Discard, "echo", "hello"); err != nil {
		t.Errorf("Exec on a mock VM: %v", err)
	}
	if err := vm.Delete(ctx); err != nil {
		t.Errorf("Delete: %v", err)
	}
}

// TestCloseEndsEventPlumbing covers the goroutines behind a pool's event
// stream — those of the tracker the set shares, which forward the pool's own
// status channel into the accounting and count what arrives — all ending when
// the set is closed, which is the only thing that ends them: the context the
// pools were created with does not, see NewPools.
func TestCloseEndsEventPlumbing(t *testing.T) {
	defer synctestutil.AssertNoGoroutinesRacy(t, 5*time.Second)()
	ctx, cancel := context.WithCancel(context.Background())
	_, pools := soloPool(ctx, t, "closing", mockPool(2, MockConfig{}))
	snapshotWithin(t, pools, "closing", 2)

	// Cancelling the creating context leaves the accounting running, since the
	// pool it accounts for is still running.
	cancel()
	snap, err := pools.PoolStatus(ctx, "closing")
	if err != nil {
		t.Fatalf("PoolStatus: %v", err)
	}
	if len(snap.Counters) == 0 {
		t.Error("no lifecycle events were counted")
	}

	if err := pools.Close(context.Background()); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Closing twice is harmless: the fan-out's own Close is idempotent.
	if err := pools.Close(context.Background()); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestEventBufferOption covers the only knob the accounting has. An unset
// buffer is passed to the tracker as it is rather than being replaced here, so
// that there is one definition of what the default is and it is the tracker's,
// see DefaultEventBuffer.
func TestEventBufferOption(t *testing.T) {
	if got, want := optionsFrom(nil).eventBuffer, 0; got != want {
		t.Errorf("unset: got %v, want %v: the tracker applies its own default", got, want)
	}
	if got, want := optionsFrom([]Option{WithEventBuffer(7)}).eventBuffer, 7; got != want {
		t.Errorf("configured: got %v, want %v", got, want)
	}
}

// TestPoolNoBackend covers a pool configured with no backend, which has
// nothing to create VMs with and so is a configuration error rather than an
// empty pool.
func TestPoolNoBackend(t *testing.T) {
	_, err := NewPools(context.Background(), map[string]PoolConfig{"nothing": {}}, discard)
	if !errors.Is(err, ErrNoBackend) {
		t.Errorf("got %v, want ErrNoBackend", err)
	}
	// The pool is named, so that the message says which one is misconfigured.
	if err == nil || !strings.Contains(err.Error(), "nothing") {
		t.Errorf("the error does not name the pool: %v", err)
	}
}

// TestPoolsLifecycle drives a set of pools through the mock backend: they are
// started, fill to their configured sizes, hand out VMs and are closed, with
// no VM technology involved.
func TestPoolsLifecycle(t *testing.T) {
	ctx := context.Background()
	cfg := map[string]PoolConfig{
		"fast": mockPool(3, MockConfig{Image: "fast-ci"}),
		"slow": mockPool(1, MockConfig{Suspendable: true}),
	}
	pools, err := NewPools(ctx, cfg, discard)
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer func() {
		if err := pools.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	if got, want := pools.Len(), 2; got != want {
		t.Errorf("len: got %v, want %v", got, want)
	}
	// The names are sorted, so that output listing the pools is stable.
	if got, want := pools.Names(), []string{"fast", "slow"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("names: got %v, want %v", got, want)
	}

	wantSizes := map[string]int{"fast": 3, "slow": 1}
	for name, size := range wantSizes {
		snap := snapshotWithin(t, pools, name, size)
		if snap.Size != size {
			t.Errorf("pool %v: size %d, want %d", name, snap.Size, size)
		}
		if snap.Acquired != 0 {
			t.Errorf("pool %v: acquired %d, want 0", name, snap.Acquired)
		}
		if snap.Kind != "mock" {
			t.Errorf("pool %v: kind %q, want mock", name, snap.Kind)
		}
	}

	// The set reports every pool, sorted by name.
	snaps, err := pools.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(snaps) != 2 || snaps[0].Name != "fast" || snaps[1].Name != "slow" {
		t.Errorf("unexpected status: %+v", snaps)
	}

	vm, err := pools.Acquire(ctx, "fast")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	after, err := pools.PoolStatus(ctx, "fast")
	if err != nil {
		t.Fatalf("PoolStatus after acquire: %v", err)
	}
	if after.Available != 2 || after.Acquired != 1 {
		t.Errorf("after acquire: available %v, acquired %v, want 2 and 1", after.Available, after.Acquired)
	}
	if err := vm.Delete(ctx); err != nil {
		t.Errorf("Delete: %v", err)
	}

	// An unconfigured pool is reported rather than panicking, by every call
	// that names one.
	if _, err := pools.Pool("nope"); err == nil {
		t.Error("an unknown pool was found")
	}
	if _, err := pools.Acquire(ctx, "nope"); err == nil {
		t.Error("acquiring from an unknown pool succeeded")
	}
	if _, err := pools.Refresh(ctx, "nope"); err == nil {
		t.Error("refreshing an unknown pool succeeded")
	}
	if err := pools.ClosePool(ctx, "nope"); err == nil {
		t.Error("closing an unknown pool succeeded")
	}
}

// TestRefreshPool covers refreshing a pool by name, which is how the web UI
// asks for one of a set to pick up a new base image. What a refresh does to
// the pool itself is basepool.Pool.Refresh's own business, and is tested
// there; this is the set finding the pool and reporting its answer.
func TestRefreshPool(t *testing.T) {
	ctx := context.Background()
	pools, err := NewPools(ctx, map[string]PoolConfig{
		"pool1": mockPool(2, MockConfig{Image: "test-img"}),
	}, discard)
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer pools.Close(ctx) //nolint:errcheck // the test has already reported anything that matters.

	snapshotWithin(t, pools, "pool1", 2)

	replaced, err := pools.Refresh(ctx, "pool1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if replaced != 2 {
		t.Fatalf("replaced %d VMs, want 2", replaced)
	}

	// Nothing is available immediately after a refresh, and the pool fills
	// itself again.
	snapshotWithin(t, pools, "pool1", 2)
}

// failingBackendConfig configures a backend whose VMs cannot be created, so
// that a pool using it fails to start.
type failingBackendConfig struct{}

func (failingBackendConfig) Validate() error { return nil }
func (failingBackendConfig) Backend(string) basepool.ProviderNew {
	return failingProviderNew{}
}

type failingProviderNew struct{}

func (failingProviderNew) Name() string { return "failing" }
func (failingProviderNew) New(context.Context) (basepool.Provider, error) {
	return failingBackend{}, nil
}

type failingBackend struct{}

var errCannotCreate = errors.New("this backend cannot create VMs")

func (failingBackend) Name() string { return "failing" }

func (failingBackend) DefaultResources() basepool.VMResources {
	return basepool.VMResources{}
}

type failingDetails struct{}

func (failingDetails) Image() ociimage.Reference { return ociimage.Reference{Name: "none"} }

func (failingBackend) Details() basepool.ProviderDetails { return failingDetails{} }

func (failingBackend) New(context.Context) (vms.Instance, error) {
	return nil, errCannotCreate
}
func (failingBackend) List(context.Context) ([]basepool.VMInfo, error) { return nil, nil }
func (failingBackend) Get(context.Context, string) (basepool.VMDetail, error) {
	return basepool.VMDetail{}, errCannotCreate
}
func (failingBackend) Delete(context.Context, time.Duration) ([]string, error) { return nil, nil }

// TestNewPoolsPartialFailure covers a set in which one pool cannot be
// started: the ones that did start are closed again rather than left running
// with nothing to reclaim them.
func TestNewPoolsPartialFailure(t *testing.T) {
	ctx := context.Background()
	good := mockPool(1, MockConfig{})
	bad := backendPool(1, failingBackendConfig{})

	pools, err := NewPools(ctx, map[string]PoolConfig{"good": good, "bad": bad},
		discard)
	if err == nil {
		t.Fatal("a set with an unstartable pool was created")
	}
	if pools != nil {
		t.Error("a set was returned alongside the error")
	}
}
