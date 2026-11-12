// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"errors"
	"time"

	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/vms/ociimage"
	basepool "cloudeng.io/vms/vmspool"
)

// PoolSnapshot is a point-in-time view of a single pool: which backend it
// uses, the VMs currently backing it, as reported by that backend, and the
// aggregate lifecycle counters accumulated from the pool's event stream.
type PoolSnapshot struct {
	Name  string
	Kind  string
	Image string
	// Size is the number of VMs the pool is configured to hold.
	Size int
	// Available, Acquired and Pending are the pool's own counts of VMs that
	// can be acquired now, are held by a caller, and are being created.
	Available int
	Acquired  int
	Pending   int
	// Completing is the number of this pool's VMs parked in the completion
	// queue, if one is attached; they have been acquired and finished with
	// but are kept for a while before being deleted.
	Completing int
	// VMs is the ground truth from the backend: the VMs that exist, whatever
	// the pool believes about them.
	VMs []basepool.VMDetail
	// Counters counts the pool's lifecycle events by kind, see
	// cloudeng.io/vms/vmspool.EventKind.
	Counters map[string]int
	// Updated is the time of the most recent event counted.
	Updated time.Time
}

// snapshot returns a snapshot of one of the set's pools: what the pool reports
// of itself, plus the lifecycle counters the set accumulated for it. The two
// halves are assembled here because the counting is the set's — every pool
// reports to one tracker, which tells them apart by the name each event
// carries — and so a pool cannot report its own counters, see
// Pools.PoolStatus.
func (p *Pools) snapshot(ctx context.Context, pool *basepool.Pool) (PoolSnapshot, error) {
	snap, failed, err := GetSnapshot(ctx, pool)
	if len(failed) > 0 {
		// Those VMs are in the snapshot with the backend's defaults standing in
		// for what could not be read, and GetSnapshot has reported them, so the
		// error is dropped: a caller handed one shows nothing at all, and a
		// single VM that cannot be inspected is no reason to blank a whole
		// pool, let alone a page listing every pool.
		err = nil
	}
	counters, updated := p.StatusTracker().Counters(pool.Name())
	snap.Counters, snap.Updated = counterNames(counters), updated
	return snap, err
}

// counterNames keys the counters by the name of each event kind rather than by
// the kind itself, which is the form a snapshot carries them in: the names are
// what a status page and the API response behind it show, and they survive an
// EventKind being renumbered, which the numbers themselves would not.
func counterNames(counters map[basepool.EventKind]int) map[string]int {
	out := make(map[string]int, len(counters))
	for kind, count := range counters {
		out[kind.String()] = count
	}
	return out
}

// ErrNocachingProvider is returned by GetSnapshot for a pool whose backend
// is not a CachingProvider.
var ErrNocachingProvider = errors.New("the pool's backend is not a vmspool.CachingProvider")

// GetSnapshot returns a point-in-time view of a single pool. That pool
// must have been created with a CachingProvider.
func GetSnapshot(ctx context.Context, pool *basepool.Pool) (PoolSnapshot, []string, error) {
	provider := pool.Provider()
	details, ok := provider.(*basepool.CachingProvider)
	if !ok {
		return PoolSnapshot{}, nil, poolError(pool.Name(), ErrNocachingProvider)
	}
	vms, failed, err := details.GetAll(ctx)
	if err != nil && len(failed) == 0 {
		// The listing itself failed, so nothing is known about any of the VMs
		// and there is no snapshot to be had.
		return PoolSnapshot{}, nil, err
	}
	if len(failed) > 0 {
		// Reported here because this is the only place that knows which VMs
		// they are; the caller decides what the snapshot is still worth.
		ctxlog.Warn(ctx, "vm details unavailable", "pool", pool.Name(), "vms", failed, "error", err)
	}
	completing := 0
	if cq := pool.CompletionQueue(); cq != nil {
		// The queue keeps that count per pool, so a queue shared by several
		// pools reports each of them only its own.
		completing = cq.NumEventsPerPool(pool.Name())
	}
	stats := pool.Stats()
	return PoolSnapshot{
		Name:       pool.Name(),
		Kind:       provider.Name(),
		Image:      imageOf(provider).String(),
		Size:       pool.Size(),
		Available:  stats.Available,
		Acquired:   stats.Acquired,
		Pending:    stats.Pending,
		Completing: completing,
		VMs:        vms,
	}, failed, err
}

// imageOf returns the image a provider's VMs are created from, or the zero
// reference for a backend that has no details to report: a provider is not
// obliged to have an image, and nothing a pool is reported through is worth a
// nil dereference.
func imageOf(provider basepool.Provider) ociimage.Reference {
	details := provider.Details()
	if details == nil {
		return ociimage.Reference{}
	}
	return details.Image()
}
