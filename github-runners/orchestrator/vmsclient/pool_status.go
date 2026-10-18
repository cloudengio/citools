// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"maps"
	"sort"
	"sync"
	"time"

	"cloudeng.io/errors"
	"cloudeng.io/sync/patterns"
	"cloudeng.io/vms/vmspool"
)

// PoolSnapshot is a point-in-time view of a single pool: its backend, the VMs
// currently backing it (the ground truth reported by the backend's Provider),
// and the aggregate lifecycle counters accumulated from the pool's event stream.
type PoolSnapshot struct {
	Name      string
	Kind      string
	Image     string
	Size      int
	Available int
	Acquired  int
	Pending   int
	VMs       []vmspool.VMDetail
	Counters  map[string]int
	Updated   time.Time
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
	maps.Copy(out, src)
	return out, t.updated[pool]
}

// Subscribe returns a change-signal channel and a cancel function that must be
// called to release the subscription. The subscription is also released when ctx
// is cancelled.
func (p *Pools) Subscribe(ctx context.Context) (<-chan struct{}, func()) {
	sub := p.tracker.pubsub.Subscribe(ctx, 1)
	return sub.C(), func() { p.tracker.pubsub.Unsubscribe(sub) }
}

// Status returns a snapshot of every configured pool, combining the per-VM
// ground truth from each pool's backend Provider with the aggregate lifecycle
// counters observed on the pool's event stream. Errors from individual backends
// are accumulated so that one failing pool does not hide the others.
func (p *Pools) Status(ctx context.Context) ([]PoolSnapshot, error) {
	out := make([]PoolSnapshot, 0, len(p.providers))
	var errs errors.M
	for name, prov := range p.providers {
		infos, err := prov.List(ctx)
		errs.Append(err)
		sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })

		poolCfg := p.configs[name]
		vms := make([]vmspool.VMDetail, len(infos))

		p.detailsMu.Lock()
		for i, info := range infos {
			detail, ok := p.detailsCache[info.Name]
			if !ok {
				var getErr error
				detail, getErr = prov.Get(ctx, info.Name)
				if getErr != nil {
					detail = vmspool.VMDetail{VMInfo: info}
				}
				// If provider returned 0 for resources, fall back to spec.
				if poolCfg.Tart != nil {
					if detail.NumCores == 0 && poolCfg.Tart.NumCPU > 0 {
						detail.NumCores = poolCfg.Tart.NumCPU
					}
					if detail.MemGiB == 0 && poolCfg.Tart.Mem > 0 {
						detail.MemGiB = int(poolCfg.Tart.Mem / (1024 * 1024 * 1024))
					}
					if detail.DiskGiB == 0 && poolCfg.Tart.Disk > 0 {
						detail.DiskGiB = int(poolCfg.Tart.Disk / (1024 * 1024 * 1024))
					}
				} else if poolCfg.Mock != nil {
					if detail.NumCores == 0 {
						detail.NumCores = 2
					}
					if detail.MemGiB == 0 {
						detail.MemGiB = 4
					}
					if detail.DiskGiB == 0 {
						detail.DiskGiB = 20
					}
				}
				p.detailsCache[info.Name] = detail
			}
			detail.VMInfo = info
			vms[i] = detail
		}
		p.detailsMu.Unlock()

		counters, updated := p.tracker.countersFor(name)
		var stats vmspool.Stats
		if pool := p.pools[name]; pool != nil {
			stats = pool.Stats()
		}
		size := poolCfg.Size
		if size == 0 && stats.Size > 0 {
			size = stats.Size
		}
		out = append(out, PoolSnapshot{
			Name:      name,
			Kind:      prov.Kind(),
			Image:     prov.Image(),
			Size:      size,
			Available: stats.Available,
			Acquired:  stats.Acquired,
			Pending:   stats.Pending,
			VMs:       vms,
			Counters:  counters,
			Updated:   updated,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, errs.Err()
}
