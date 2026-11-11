// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"io"
	"sync"

	"cloudeng.io/errors"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/sync/errgroup"
	basepool "cloudeng.io/vms/vmspool"
)

// Pools is a set of named pools, each with a backend of its own, so that
// pools backed by different VM technologies run side by side. It is a
// basepool.Set plus what this package adds: the configured backends the pools
// are created from, see BackendConfig, and the status and capacity reporting
// the web UI and the webhook listener need, see PoolSnapshot and
// Pools.HasCapacity.
//
// The embedded set owns the pools, the completion queue and the status
// tracker, and supplies everything that is not specific to the orchestrator:
// addressing a pool by name, acquiring and refreshing, the change signal and
// closing. Only Close is overridden, to log it.
type Pools struct {
	*basepool.Set
}

// NewPools creates and starts every configured pool and returns them as a
// set, which is the only way a pool is created: a pool reports to the set's
// accounting, so a single pool is a set of one. The pools are created
// concurrently, since filling one means creating VMs and that is slow.
// createFile supplies the writer that a VM's output is recorded to, given the
// VM's ID.
//
// It is all or nothing. If any pool cannot be created or started, those that
// were are closed again and the error is returned: a half-started set would
// otherwise leave VMs running with nothing left to reclaim them.
func NewPools(ctx context.Context, cfgs map[string]PoolConfig, createFile func(string) io.Writer, opts ...Option) (*Pools, error) {
	// First, so that an option may still displace it, see WithBaseOptions.
	o := optionsFrom(append([]Option{
		WithBaseOptions(basepool.WithStdoutStderr(createFile, createFile)),
	}, opts...))
	// One tracker for the set, which tells its pools apart by the name each
	// event carries; the buffer is how bursty a pool may be before its events
	// are dropped rather than counted.
	tracker := basepool.NewStatusTracker(ctx, o.eventBuffer)
	// One queue for the set, so that a parked VM is released once, by the set,
	// rather than by whichever pool sharing it is closed first. No outcomes
	// means no queue, which tells an application to delete each VM itself.
	if len(o.outcomes) > 0 {
		o.completion = basepool.NewCompletionQueue(ctx, o.outcomes...)
	}
	var mu sync.Mutex
	started := make([]*basepool.Pool, 0, len(cfgs))
	var g errgroup.T
	for name, cfg := range cfgs {
		g.Go(func() error {
			pool, err := newPool(ctx, name, cfg, tracker, o)
			if err != nil {
				return err
			}
			mu.Lock()
			started = append(started, pool)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		var errs errors.M
		errs.Append(err)
		// The pools that did start, the queue and the tracker are released by
		// closing a set made of them, which is what knows the order to do it
		// in. There is no set to hand back, so it is closed here.
		ctxlog.Info(ctx, "closing the pools that started before another failed", "pools", len(started))
		errs.Append(basepool.NewSet(o.completion, tracker, started...).Close(ctx))
		return nil, errs.Err()
	}
	return &Pools{Set: basepool.NewSet(o.completion, tracker, started...)}, nil
}

// ClosePool closes the named pool, leaving the rest of the set running. The
// completion queue is not closed, since the VMs it holds may have come from
// any pool; this pool's are released when the queue expires them or the set is
// closed, see Pools.Close.
func (p *Pools) ClosePool(ctx context.Context, name string) error {
	pool, err := p.Pool(name)
	if err != nil {
		return err
	}
	ctxlog.Info(ctx, "closing vm pool", "pool", name)
	return pool.Close(ctx)
}

// Close closes every pool, then the completion queue and the accounting they
// share, see basepool.Set.Close. It is idempotent.
func (p *Pools) Close(ctx context.Context) error {
	ctxlog.Info(ctx, "closing vm pools", "pools", p.Len())
	err := p.Set.Close(ctx)
	if err != nil {
		ctxlog.Error(ctx, "failed to close the vm pools", "error", err)
		return err
	}
	ctxlog.Info(ctx, "closed the vm pools")
	return nil
}

// Status returns a snapshot of every pool, sorted by name, accumulating the
// backends' errors so that one failing backend does not hide what the others
// report. A closed set reports its pools as a running one does.
func (p *Pools) Status(ctx context.Context) ([]PoolSnapshot, error) {
	pools := p.All()
	out := make([]PoolSnapshot, 0, len(pools))
	var errs errors.M
	// Names is sorted, so no sort is needed afterwards.
	for _, name := range p.Names() {
		snap, err := p.snapshot(ctx, pools[name])
		errs.Append(err)
		out = append(out, snap)
	}
	return out, errs.Err()
}

// PoolStatus returns a snapshot of the named pool. It is the set that reports
// a pool because the lifecycle counters in a snapshot are the set's tracker's,
// see NewPools.
func (p *Pools) PoolStatus(ctx context.Context, name string) (PoolSnapshot, error) {
	pool, err := p.Pool(name)
	if err != nil {
		return PoolSnapshot{}, err
	}
	return p.snapshot(ctx, pool)
}
