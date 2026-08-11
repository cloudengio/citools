// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"cloudeng.io/macos/tartvm"
	"cloudeng.io/sync/patterns"
	"cloudeng.io/vms/vmspool"
)

// VMSnapshot is a point-in-time view of a single VM within a pool, derived from
// "tart list" (see ListTartVMs). It is the ground truth for per-VM state; the
// pool event stream only carries aggregate lifecycle counters, not VM identity.
type VMSnapshot struct {
	Name     string
	State    string
	Running  bool
	Source   string
	DiskGiB  int
	Accessed time.Time
}

// PoolSnapshot is a point-in-time view of a single pool: its configuration, the
// VMs currently backing it, and the aggregate lifecycle counters accumulated
// from the pool's event stream.
type PoolSnapshot struct {
	Name     string
	Image    string
	Size     int
	VMs      []VMSnapshot
	Counters map[string]int
	Updated  time.Time
}

// PoolStatusTracker accumulates per-pool lifecycle event counters from the
// pools' event streams and notifies subscribers when the aggregate state
// changes. It is safe for concurrent use.
type PoolStatusTracker struct {
	mu       sync.Mutex
	counters map[string]map[string]int
	updated  map[string]time.Time
	pubsub   *patterns.PubSub[struct{}]
}

func newPoolStatusTracker() *PoolStatusTracker {
	return &PoolStatusTracker{
		counters: make(map[string]map[string]int),
		updated:  make(map[string]time.Time),
		pubsub:   patterns.New[struct{}](),
	}
}

func (t *PoolStatusTracker) record(pool string, e vmspool.Event) {
	t.mu.Lock()
	c := t.counters[pool]
	if c == nil {
		c = make(map[string]int)
		t.counters[pool] = c
	}
	c[e.Kind.String()]++
	t.updated[pool] = e.Time
	t.mu.Unlock()
	t.pubsub.Publish(struct{}{})
}

func (t *PoolStatusTracker) countersFor(pool string) (map[string]int, time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	src := t.counters[pool]
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out, t.updated[pool]
}

// Subscribe returns a change-signal channel and a cancel function that must be
// called to release the subscription. The subscription is also released when ctx
// is cancelled.
func (p *Pools) Subscribe(ctx context.Context) (<-chan struct{}, func()) {
	sub := p.tracker.pubsub.Subscribe(ctx, 1)
	return sub.C(), func() { p.tracker.pubsub.Unsubscribe(sub) }
}

// poolForVMName returns the name of the configured pool whose VM naming
// convention matches vmName, or "" if none match.
func (p *Pools) poolForVMName(vmName string) string {
	for name, cfg := range p.configs {
		if cfg.TartConfig.Image == "" {
			continue
		}
		if strings.HasPrefix(vmName, vmNamePrefix(name, cfg.TartConfig.Image)) {
			return name
		}
	}
	return ""
}

func vmSnapshotFromEntry(e tartvm.ListEntry) VMSnapshot {
	return VMSnapshot{
		Name:     e.Name,
		State:    e.State,
		Running:  e.Running,
		Source:   e.Source,
		DiskGiB:  e.Disk,
		Accessed: e.Accessed,
	}
}

// Status returns a snapshot of every configured pool, combining the per-VM
// ground truth from "tart list" with the aggregate lifecycle counters observed
// on each pool's event stream.
func (p *Pools) Status(ctx context.Context) ([]PoolSnapshot, error) {
	entries, err := ListTartVMs(ctx, p.configs)
	if err != nil {
		return nil, err
	}
	byPool := make(map[string][]VMSnapshot)
	for _, e := range entries {
		pool := p.poolForVMName(e.Name)
		byPool[pool] = append(byPool[pool], vmSnapshotFromEntry(e))
	}
	out := make([]PoolSnapshot, 0, len(p.configs))
	for name, cfg := range p.configs {
		counters, updated := p.tracker.countersFor(name)
		vms := byPool[name]
		sort.Slice(vms, func(i, j int) bool { return vms[i].Name < vms[j].Name })
		out = append(out, PoolSnapshot{
			Name:     name,
			Image:    cfg.TartConfig.Image,
			Size:     cfg.Config.Size,
			VMs:      vms,
			Counters: counters,
			Updated:  updated,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
