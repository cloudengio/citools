// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"testing"
	"time"

	"cloudeng.io/macos/tartvm"
	"cloudeng.io/vms/vmspool"
)

func testPools() *Pools {
	return &Pools{
		configs: map[string]PoolConfig{
			"macos": {TartConfig: TartConfig{Image: "macos-ci"}},
			"linux": {TartConfig: TartConfig{Image: "linux-ci"}},
		},
		tracker: newPoolStatusTracker(),
	}
}

func TestPoolForVMName(t *testing.T) {
	p := testPools()
	cases := map[string]string{
		vmNamePrefix("macos", "macos-ci") + "20260101-000000-0001": "macos",
		vmNamePrefix("linux", "linux-ci") + "20260101-000000-0002": "linux",
		"some-unrelated-vm": "",
		// right pool name but wrong image must not match.
		vmNamePrefix("macos", "linux-ci") + "x": "",
	}
	for name, want := range cases {
		if got := p.poolForVMName(name); got != want {
			t.Errorf("poolForVMName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestVMSnapshotFromEntry(t *testing.T) {
	acc := time.Now()
	e := tartvm.ListEntry{Name: "vm1", State: "running", Running: true, Source: "local", Disk: 50, Accessed: acc}
	s := vmSnapshotFromEntry(e)
	if s.Name != "vm1" || !s.Running || s.State != "running" || s.DiskGiB != 50 || !s.Accessed.Equal(acc) {
		t.Errorf("unexpected snapshot: %+v", s)
	}
}

func TestPoolStatusTrackerCounters(t *testing.T) {
	tr := newPoolStatusTracker()
	tr.record("macos", vmspool.Event{Kind: vmspool.EventVMCreated, Time: time.Now()})
	tr.record("macos", vmspool.Event{Kind: vmspool.EventVMCreated, Time: time.Now()})
	tr.record("macos", vmspool.Event{Kind: vmspool.EventAcquired, Time: time.Now()})

	counters, updated := tr.countersFor("macos")
	if counters[vmspool.EventVMCreated.String()] != 2 {
		t.Errorf("EventVMCreated count = %d, want 2", counters[vmspool.EventVMCreated.String()])
	}
	if counters[vmspool.EventAcquired.String()] != 1 {
		t.Errorf("EventAcquired count = %d, want 1", counters[vmspool.EventAcquired.String()])
	}
	if updated.IsZero() {
		t.Error("updated timestamp not set")
	}
	// countersFor must return a copy, not the internal map.
	counters["injected"] = 99
	if again, _ := tr.countersFor("macos"); again["injected"] != 0 {
		t.Error("countersFor returned a mutable reference to internal state")
	}
}

func TestPoolStatusTrackerNotifies(t *testing.T) {
	tr := newPoolStatusTracker()
	sub := tr.pubsub.Subscribe(context.Background(), 1)
	defer tr.pubsub.Unsubscribe(sub)
	tr.record("macos", vmspool.Event{Kind: vmspool.EventAcquired, Time: time.Now()})
	select {
	case <-sub.C():
	case <-time.After(time.Second):
		t.Fatal("expected a change notification")
	}
}
