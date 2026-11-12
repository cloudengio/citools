// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

// Package vmsclient runs the VM pools that the orchestrator runs its jobs on,
// on top of the pool implementation in cloudeng.io/vms/vmspool. That package
// creates, fills and drains a single pool of VMs; this one adds what the
// orchestrator needs around it:
//
//   - the backends it supports, tart and the mock, as
//     cloudeng.io/vms/vmspool.Provider implementations, and the configuration
//     file syntax that selects and configures one of them for each pool, see
//     PoolConfig and BackendConfig,
//   - the configuration of a whole set of pools, so that pools backed by
//     different technologies are configured and created together, see
//     NewPools. Holding them afterwards, addressing them by name, acquiring
//     and refreshing, reporting changes and closing is
//     cloudeng.io/vms/vmspool.Set, which Pools embeds,
//   - the status snapshots the web UI needs, see PoolSnapshot, whose lifecycle
//     counters are a basepool.StatusTracker's, one of which the whole set
//     shares, and the capacity the webhook listener's connection is gated on,
//     see Pools.HasCapacity,
//   - the creation and lifetime of a completion queue, which parks the VMs of
//     finished jobs for a while so that they can be inspected before they are
//     deleted, see WithCompletionOutcomes. The queue itself is
//     basepool.CompletionQueue, a component in its own right; a set creates
//     one and hands it to its pools, which hand it to the VMs they create and
//     count the ones it is holding. Releasing it, after the pools that park
//     VMs in it have stopped, is basepool.Set.Close's doing,
//   - the directory the GitHub Actions runner is installed in on the guest,
//     which is a property of the image rather than anything a pool needs to
//     know about, see PoolConfig.RunnerDir.
//
// Nothing but the backends depends on a particular VM technology. Supporting
// another one means implementing basepool.Provider, the BackendConfig that
// builds it and a section for it in PoolConfig, and nothing else; the pools,
// the status reporting and the completion queue are unchanged by it.
//
// Pools are created with NewPools, which is the only way to create one: the
// pools of a set share the accounting of their events — every event names the
// pool that emitted it, see basepool.Event.Pool, so one
// basepool.StatusTracker counts all of them and its change signal covers all
// of them — and the completion queue their finished VMs are parked in. A
// single pool is a set of one, and pays for nothing more.
package vmsclient

import (
	"context"
	"log/slog"

	"cloudeng.io/errors"
	"cloudeng.io/logging/ctxlog"
	basepool "cloudeng.io/vms/vmspool"
)

// ErrMultipleBackends is returned when a pool configuration selects more than
// one VM backend, which would leave the choice of backend ambiguous.
var ErrMultipleBackends = errors.New("more than one VM backend configured (set exactly one of tart_config or mock_config)")

// PoolConfig configures a single VM pool: the generic pool settings of
// cloudeng.io/vms/vmspool.Config, which govern how large the pool is and how
// its VMs are created, staged and reclaimed, plus exactly one backend section
// that selects and configures the VM technology (eg. tart) that backs it.
// Different pools may use different backends.
type PoolConfig struct {
	basepool.Config `yaml:",inline"`
	Tart            *TartConfig `yaml:"tart_config" doc:"configure and select the tart VM backend for this pool"`
	Mock            *MockConfig `yaml:"mock_config" doc:"configure and select the mock VM backend for this pool, for exercising the orchestrator without any VM technology installed"`
	// Backend selects the backend directly, for a caller that assembles a
	// PoolConfig rather than reading one: it comes from no configuration file,
	// and counts as the one backend a pool may have.
	Backend BackendConfig `yaml:"-"`
}

// RunnerDir returns the guest directory in which the GitHub runner is installed
// for this pool's backend, or "" if no backend is configured.
func (cfg PoolConfig) RunnerDir() string {
	switch {
	case cfg.Tart != nil:
		return cfg.Tart.RunnerDir
	case cfg.Mock != nil:
		return cfg.Mock.RunnerDir
	}
	return ""
}

// backendConfig returns the configuration of the one backend the pool selects,
// which is what makes the pool usable.
func (cfg PoolConfig) backendConfig() (BackendConfig, error) {
	selected := 0
	for _, set := range []bool{cfg.Backend != nil, cfg.Tart != nil, cfg.Mock != nil} {
		if set {
			selected++
		}
	}
	switch {
	case selected > 1:
		return nil, ErrMultipleBackends
	case cfg.Backend != nil:
		return cfg.Backend, nil
	case cfg.Tart != nil:
		return *cfg.Tart, nil
	case cfg.Mock != nil:
		return *cfg.Mock, nil
	}
	return nil, ErrNoBackend
}

// Validate ensures that exactly one VM backend is configured for the pool and
// that its required settings are present, so that a mistake in a configuration
// file is caught when it is read rather than when the pool first tries to
// create a VM.
func (cfg PoolConfig) Validate() error {
	bc, err := cfg.backendConfig()
	if err != nil {
		return err
	}
	return bc.Validate()
}

// backend validates the configuration and returns what the named pool's
// backend is created by, annotating a failure with that name: a message that
// does not say which pool is misconfigured is of little use when several are.
// Nothing is created here, so a caller that wants no more than the kind of VM
// technology the pool is configured for asks for it and stops, see
// basepool.ProviderNew.
func (cfg PoolConfig) backend(name string) (basepool.ProviderNew, error) {
	bc, err := cfg.backendConfig()
	if err != nil {
		return nil, poolError(name, err)
	}
	if err := bc.Validate(); err != nil {
		return nil, poolError(name, err)
	}
	return bc.Backend(name), nil
}

// newBackend validates the configuration and builds the backend for the named
// pool, annotating either failure with that name.
func (cfg PoolConfig) newBackend(ctx context.Context, name string) (basepool.Provider, error) {
	pn, err := cfg.backend(name)
	if err != nil {
		return nil, err
	}
	b, err := pn.New(ctx)
	if err != nil {
		return nil, poolError(name, err)
	}
	return b, nil
}

// loggerFor returns the logger a backend captures for its lifetime: the one
// carried by ctx, which is how the orchestrator's logging configuration
// reaches the VM lifecycle.
func loggerFor(ctx context.Context) *slog.Logger {
	return ctxlog.Logger(ctx)
}
