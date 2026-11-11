// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"fmt"
	"slices"

	"cloudeng.io/logging/ctxlog"
	basepool "cloudeng.io/vms/vmspool"
)

// newPool creates the backend for one named pool, starts the pool and returns
// it. Start waits for the first VM to be ready; the rest are created in the
// background, so a pool is usable before it is full.
//
// A pool is a cloudeng.io/vms/vmspool.Pool and nothing besides: everything
// this package adds to one it adds for the whole set, so there is no per-pool
// state to wrap it in. The configuration is spent here, on the backend and the
// pool's own options; what a pool is afterwards — its kind, its image, the VMs
// it has — is read back from the backend when it is reported, see GetSnapshot.
// A pool is reached through the set that owns it, by name, see Pools: the
// lifecycle counters in its snapshot are the set's, see Pools.PoolStatus, and
// so is the completion queue it hands to the VMs it creates, see
// Pools.CompletionQueue, so a pool cannot answer for either on its own.
//
// The name is the pool's identity: it appears in status output and in errors,
// and a backend includes it in the names of the VMs it creates. tracker is the
// set's accounting, which the pool is connected to by the option it hands out:
// the channel the pool reports on is the tracker's, counted from before the
// pool exists, so nothing a pool reports as it fills goes uncounted.
func newPool(ctx context.Context, name string, cfg PoolConfig, tracker *basepool.StatusTracker, o options) (*basepool.Pool, error) {
	backend, err := cfg.newBackend(ctx, name)
	if err != nil {
		return nil, err
	}
	// The backend is wrapped so that the unchanging details of a VM are read
	// from it once rather than on every status request: inspecting a tart VM
	// costs a subprocess, and a status page is polled. The pool is given the
	// wrapper, which is a provider itself, so that the kind, the image and the
	// VMs of a snapshot all reach the backend through the one object the pool
	// reports, see basepool.Pool.Provider; it caches nothing the pool itself
	// asks for, which is only the creation of VMs.
	cache := &basepool.CachingProvider{Provider: backend}
	// The configured options come first so that the ones this package owns
	// cannot be displaced by them, see WithBaseOptions.
	baseOpts := slices.Clone(cfg.Options())
	baseOpts = append(baseOpts, o.baseOpts...)
	baseOpts = append(baseOpts, tracker.Track(ctx))
	if o.completion != nil {
		// The pool holds the set's queue so that every VM it hands out reports
		// it, see basepool.VM.CompletionQueue: that is how work that has
		// finished with a VM finds the queue to park it in. The pool does not
		// close it, see Pools.Close.
		baseOpts = append(baseOpts, basepool.WithCompletionQueue(o.completion))
	}
	p := basepool.New(cache, name, baseOpts...)

	ctxlog.Info(ctx, "starting vm pool", "pool", name, "kind", cache.Name(), "image", imageOf(cache).String())
	if err := p.Start(ctx); err != nil {
		// Nothing was handed out, so there is nothing to reclaim here. The
		// accounting is the set's, which closes it when it gives up on the
		// pools that did start, see NewPools.
		ctxlog.Error(ctx, "failed to start vm pool", "pool", name, "error", err)
		return nil, poolError(name, fmt.Errorf("failed to start: %w", err))
	}
	ctxlog.Info(ctx, "vm pool started", "pool", name)
	return p, nil
}
