// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"time"

	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/macos/tartvm"
	"cloudeng.io/sync/errgroup"
	"cloudeng.io/vms"
	"cloudeng.io/vms/vmspool"
)

type TartConstructor struct {
	name   string
	image  string
	logger *slog.Logger
	opts   []tartvm.Option
}

func (c *TartConstructor) New() vms.Instance {
	name := fmt.Sprintf("%s-%s-%s", c.name, c.image, time.Now().Format("20060102-150405"))
	opts := slices.Clone(c.opts)
	opts = append(opts, tartvm.WithLogger(c.logger), tartvm.WithObtainIPAtStart(false))
	vm := tartvm.New(context.Background(), c.image, name, opts...)
	return vm
}

func NewTartConstructor(ctx context.Context, name, image string, opts ...tartvm.Option) *TartConstructor {
	return &TartConstructor{
		name:   name,
		image:  image,
		opts:   opts,
		logger: ctxlog.Logger(ctx),
	}
}

type TartConfig struct {
	tartvm.Config `yaml:",inline"`
	Image         string `yaml:"image" doc:"base image to use for cloning VMs in this pool"`
	RunnerDir     string `yaml:"runner_dir" doc:"directory on the VM in which the runner was installed, specific to each type of image."`
}

type PoolConfig struct {
	vmspool.Config `yaml:",inline"`
	TartConfig     TartConfig `yaml:"tart_config" doc:"configuration for tart VM pools"`
}

func (cfg PoolConfig) RunnerDir() string {
	if cfg.TartConfig.Image != "" {
		return cfg.TartConfig.RunnerDir
	}
	return ""
}

func (cfg PoolConfig) Validate() error {
	numTypes := 0
	if cfg.TartConfig.Image != "" {
		numTypes++
	}
	if numTypes == 0 {
		return fmt.Errorf("exactly one of tart_config, or other VM pool types must be specified")
	}
	return nil
}

type Pools struct {
	pools map[string]*vmspool.Pool
	start map[string]*sync.Once
	mu    sync.Mutex
}

func NewPools(ctx context.Context, cfg map[string]PoolConfig, createFile func(string) io.Writer) (*Pools, error) {
	p := &Pools{
		pools: make(map[string]*vmspool.Pool),
		start: make(map[string]*sync.Once),
	}

	for name, poolCfg := range cfg {
		var constructor vmspool.Constructor
		if poolCfg.TartConfig.Image != "" {
			fmt.Printf("creating tart constructor for pool %s with image %s\n", name, poolCfg.TartConfig.Image)
			constructor = NewTartConstructor(ctx, name, poolCfg.TartConfig.Image, poolCfg.TartConfig.Options()...)
		}
		if constructor == nil {
			return nil, fmt.Errorf("vm_pool %s: no constructor could be created for the specified configuration", name)
		}
		eventCh := make(chan vmspool.Event, 100)
		go func() {
			for e := range eventCh {
				ctxlog.Info(ctx, "vm pool event", "pool", name, "event", e.Kind)
			}
		}()
		opts := slices.Clone(poolCfg.Options())
		opts = append(opts, vmspool.WithStatus(eventCh),
			vmspool.WithStdoutStderr(createFile, createFile))
		p.pools[name] = vmspool.New(constructor, opts...)
		p.start[name] = &sync.Once{}
	}
	return p, nil
}

func (p *Pools) GetVM(ctx context.Context, name string) (*vmspool.VM, error) {
	vmp := p.pools[name]
	var err error
	p.start[name].Do(func() {
		ctxlog.Info(ctx, "starting vm pool", "pool", name)
		err = vmp.Start(ctx)
		if err != nil {
			ctxlog.Error(ctx, "failed to start vm pool", "pool", name, "error", err)
		} else {
			ctxlog.Info(ctx, "started vm pool", "pool", name)
		}
	})
	if err != nil {
		return nil, err
	}
	return vmp.Acquire(ctx)
}

func (p *Pools) ClosePool(ctx context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	vmp, ok := p.pools[name]
	if !ok {
		return fmt.Errorf("vm_pool %s: no such pool", name)
	}
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
