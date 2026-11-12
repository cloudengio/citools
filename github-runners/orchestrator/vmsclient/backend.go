// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"cloudeng.io/errors"
	basepool "cloudeng.io/vms/vmspool"
)

// ErrNoBackend is returned for a pool configuration that selects no backend,
// and so has nothing to create VMs with.
var ErrNoBackend = errors.New("no VM backend configured")

// BackendConfig is the configuration of a pool's backend: it validates itself
// and names what builds the backend it describes. TartConfig and MockConfig
// are the two, each a section of a PoolConfig.
//
// A backend is a basepool.Provider, the VM technology that creates, lists,
// inspects and deletes the pool's VMs. It is all a pool knows of the
// technology underneath it, so supporting a new one means implementing that
// interface, the BackendConfig that builds it and a section for it in
// PoolConfig, and changing nothing else. Its Name is the VM technology, eg.
// "tart", and its Details report the base image the pool's VMs are created
// from; PoolSnapshot.Kind and PoolSnapshot.Image are those two, named as the
// web UI reports them. Its DefaultResources state what its VMs are created
// with, which Pools.Status uses to fill in the figures a VM does not report
// for itself.
type BackendConfig interface {
	// Validate reports whether the configuration is usable, so that a mistake
	// in a configuration file is caught when it is read rather than when the
	// pool first tries to create a VM.
	Validate() error
	// Backend returns what the named pool's backend is created by: a
	// basepool.ProviderNew, which names the VM technology and builds the
	// provider that implements it. Calling it creates nothing — the backend
	// itself is built by basepool.ProviderNew.New, whose context carries the
	// logger the backend captures for its lifetime, see ctxlog.Logger — so the
	// kind of a configured pool can be reported without a backend being built
	// for it.
	//
	// The pool's name is bound here rather than passed to New, which builds a
	// provider from nothing but a context: a configuration does not carry the
	// name of the pool it configures, that being the key it is stored under,
	// and a backend needs the name because by convention it includes it in the
	// names of the VMs it creates, so that the VMs of one pool can be told
	// from another's and those left behind by a previous run can be found
	// again.
	Backend(pool string) basepool.ProviderNew
}

// poolError annotates err with the name of the pool it concerns.
func poolError(name string, err error) error {
	return fmt.Errorf("vm_pool %s: %w", name, err)
}

// backends returns what the configured pools' VMs are created by, in name
// order. It is all or nothing: a configuration that names an unusable backend
// is a mistake in the configuration, and reporting it is more use than
// sweeping the pools that happen to be fine.
func backends(cfgs map[string]PoolConfig) ([]basepool.ProviderNew, error) {
	out := make([]basepool.ProviderNew, 0, len(cfgs))
	for _, name := range slices.Sorted(maps.Keys(cfgs)) {
		pn, err := cfgs[name].backend(name)
		if err != nil {
			return nil, err
		}
		out = append(out, pn)
	}
	return out, nil
}

// ListPoolVMs returns the VMs that currently exist for every configured pool,
// across all of their backends, see basepool.ListVMs. It needs no running
// pool, so it reports the VMs left behind by a previous run as readily as the
// ones in use by this one.
func ListPoolVMs(ctx context.Context, cfgs map[string]PoolConfig) ([]basepool.VMInfo, error) {
	pns, err := backends(cfgs)
	if err != nil {
		return nil, err
	}
	return basepool.ListVMs(ctx, pns...)
}

// DeletePoolVMs deletes the VMs of every configured pool, across all of their
// backends, and returns the names of those deleted, see basepool.DeleteVMs.
// Like ListPoolVMs it needs no running pool, which is what lets it clean up
// after a run that exited without doing so itself.
func DeletePoolVMs(ctx context.Context, cfgs map[string]PoolConfig, stopTimeout time.Duration) ([]string, error) {
	pns, err := backends(cfgs)
	if err != nil {
		return nil, err
	}
	return basepool.DeleteVMs(ctx, stopTimeout, pns...)
}
