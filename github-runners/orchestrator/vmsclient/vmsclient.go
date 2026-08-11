// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"fmt"
	"io"
	"slices"

	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/sync/errgroup"
	"cloudeng.io/vms/vmspool"
)

// PoolConfig configures a single VM pool: the generic vmspool settings plus
// exactly one backend section that selects and configures the VM technology
// (e.g. tart) that backs the pool. Different pools may use different backends.
type PoolConfig struct {
	vmspool.Config `yaml:",inline"`
	Tart           *TartConfig `yaml:"tart_config" doc:"configure and select the tart VM backend for this pool"`
}

// RunnerDir returns the guest directory in which the GitHub runner is installed
// for this pool's backend, or "" if no backend is configured.
func (cfg PoolConfig) RunnerDir() string {
	if cfg.Tart != nil {
		return cfg.Tart.RunnerDir
	}
	return ""
}

// Validate ensures a VM backend is configured for the pool.
func (cfg PoolConfig) Validate() error {
	if cfg.Tart == nil {
		return ErrNoBackend
	}
	if cfg.Tart.Image == "" {
		return fmt.Errorf("tart_config.image is required")
	}
	if cfg.Tart.RunnerDir == "" {
		return fmt.Errorf("tart_config.runner_dir is required")
	}
	return nil
}

type Pool struct {
	*vmspool.Pool
	name string
}

func (p *Pool) Name() string {
	return p.name
}

// Pools is a set of named VM pools, each backed by its own Provider so that
// pools using different VM technologies can run side by side.
type Pools struct {
	pools     map[string]*Pool
	configs   map[string]PoolConfig
	providers map[string]Provider
	tracker   *PoolStatusTracker
}

func NewPools(ctx context.Context, cfg map[string]PoolConfig, createFile func(string) io.Writer) (*Pools, error) {
	providers, err := newProviders(ctx, cfg)
	if err != nil {
		return nil, err
	}
	p := &Pools{
		pools:     make(map[string]*Pool),
		configs:   cfg,
		providers: providers,
		tracker:   newPoolStatusTracker(),
	}
	var g errgroup.T
	for name, poolCfg := range cfg {
		provider := providers[name]
		eventCh := make(chan vmspool.Event, 100)
		go func() {
			for e := range eventCh {
				p.tracker.record(name, e)
				ctxlog.Info(ctx, "vm pool event", "pool", name, "event", e.Kind)
			}
		}()
		opts := slices.Clone(poolCfg.Options())
		opts = append(opts,
			vmspool.WithStatus(eventCh),
			vmspool.WithStdoutStderr(createFile, createFile))
		vmp := &Pool{
			Pool: vmspool.New(provider, opts...),
			name: name,
		}
		p.pools[name] = vmp
		g.Go(func() error {
			ctxlog.Info(ctx, "starting vm pool", "pool", name)
			if err := vmp.Start(ctx); err != nil {
				ctxlog.Error(ctx, "failed to start vm pool", "pool", name, "error", err)
				return fmt.Errorf("vm_pool %s: failed to start: %w", name, err)
			}
			ctxlog.Info(ctx, "vm pool started", "pool", name)
			return nil
		})
	}
	return p, g.Wait()
}

func (p *Pools) Acquire(ctx context.Context, name string) (*vmspool.VM, error) {
	vmp, ok := p.pools[name]
	if !ok {
		return nil, fmt.Errorf("vm_pool %s: no such pool", name)
	}
	return vmp.Acquire(ctx)
}

func (p *Pools) ClosePool(ctx context.Context, name string) error {
	vmp, ok := p.pools[name]
	if !ok {
		return fmt.Errorf("vm_pool %s: no such pool", name)
	}
	ctxlog.Info(ctx, "closing vm pool", "pool", name)
	return vmp.Close(ctx)
}

func (p *Pools) Close(ctx context.Context) error {
	var g errgroup.T
	for _, vmp := range p.pools {
		g.Go(func() error {
			return vmp.Close(ctx)
		})
	}
	return g.Wait()
}
