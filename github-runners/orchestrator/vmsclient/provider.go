// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"cloudeng.io/errors"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/vms/vmspool"
)

// ErrNoBackend is returned when a pool configuration selects no VM backend.
var ErrNoBackend = errors.New("no VM backend configured (set tart_config or mock_config)")

// ErrMultipleBackends is returned when a pool configuration selects more than
// one VM backend, which would leave the choice of backend ambiguous.
var ErrMultipleBackends = errors.New("more than one VM backend configured (set exactly one of tart_config or mock_config)")

// poolError annotates err with the pool name.
func poolError(name string, err error) error {
	return fmt.Errorf("vm_pool %s: %w", name, err)
}

// Provider is the orchestrator's VM backend abstraction. It is a vmspool.Provider
// (construct/list/get/delete the pool's VMs) extended with the orchestrator
// specific metadata the rest of the system needs. Backing pools with a
// technology other than tart requires only implementing this interface and
// adding a corresponding backend section to PoolConfig. Because each pool
// selects its own provider, pools using different backends run simultaneously.
type Provider interface {
	vmspool.Provider
	// Kind returns the backend kind, e.g. "tart".
	Kind() string
	// Image returns the base image identifier, or "" if the backend has no such
	// concept.
	Image() string
	// RunnerDir returns the directory on the guest where the GitHub Actions
	// runner is installed.
	RunnerDir() string
}

// newProvider returns the Provider selected by cfg's configured backend section.
func (cfg PoolConfig) newProvider(name string, logger *slog.Logger) (Provider, error) {
	switch {
	case cfg.Tart != nil && cfg.Mock != nil:
		return nil, ErrMultipleBackends
	case cfg.Tart != nil:
		return newTartProvider(name, *cfg.Tart, logger), nil
	case cfg.Mock != nil:
		return newMockProvider(name, *cfg.Mock, logger), nil
	default:
		return nil, ErrNoBackend
	}
}

// newProviders builds a Provider for every configured pool.
func newProviders(ctx context.Context, cfg map[string]PoolConfig) (map[string]Provider, error) {
	logger := ctxlog.Logger(ctx)
	out := make(map[string]Provider, len(cfg))
	for name, poolCfg := range cfg {
		p, err := poolCfg.newProvider(name, logger)
		if err != nil {
			return nil, poolError(name, err)
		}
		out[name] = p
	}
	return out, nil
}

// ListPoolVMs returns the VMs currently present for every configured pool,
// across all backends. It is used to enumerate the VMs left behind by a previous
// run without a live Pools instance.
func ListPoolVMs(ctx context.Context, cfg map[string]PoolConfig) ([]vmspool.VMInfo, error) {
	providers, err := newProviders(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var out []vmspool.VMInfo
	var errs errors.M
	for _, p := range providers {
		vms, err := p.List(ctx)
		errs.Append(err)
		out = append(out, vms...)
	}
	return out, errs.Err()
}

// DeletePoolVMs deletes the VMs for every configured pool, across all backends,
// returning the names deleted.
func DeletePoolVMs(ctx context.Context, cfg map[string]PoolConfig, stopTimeout time.Duration) ([]string, error) {
	providers, err := newProviders(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var deleted []string
	var errs errors.M
	for _, p := range providers {
		names, err := p.Delete(ctx, stopTimeout)
		errs.Append(err)
		deleted = append(deleted, names...)
	}
	return deleted, errs.Err()
}
