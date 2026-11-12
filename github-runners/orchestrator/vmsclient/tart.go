// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"

	"cloudeng.io/macos/tartvm"
	"cloudeng.io/vms"
	"cloudeng.io/vms/ociimage"
	basepool "cloudeng.io/vms/vmspool"
)

// TartConfig configures the tart VM backend for a pool: how tart itself is
// run, which image its VMs are cloned from and how that image is obtained,
// the resources the VMs are given, and where the runner lives inside them.
type TartConfig struct {
	tartvm.Config        `yaml:",inline"`
	ociimage.Source      `yaml:",inline"`
	basepool.VMResources `yaml:",inline"`
	RunnerDir            string `yaml:"runner_dir" doc:"directory on the VM in which the runner was installed, specific to each type of image."`
}

// TartKind names the tart VM technology, as both the configuration that
// selects it and the backend that implements it report it. It is tartvm's own
// name for itself, so that the two cannot drift apart.
const TartKind = tartvm.ProviderName

// Validate implements BackendConfig. An image and the runner
// directory within it are both required: tart has nothing to clone without
// the first, and the orchestrator has nowhere to install the runner without
// the second. The image must also be a reference that can be parsed, so that
// one that cannot is reported when the configuration file is read rather than
// when the first VM fails to be cloned.
func (cfg TartConfig) Validate() error {
	if cfg.Image == "" {
		return fmt.Errorf("tart_config.image is required")
	}
	if _, err := cfg.Reference(); err != nil {
		return fmt.Errorf("tart_config.image: %w", err)
	}
	if cfg.RunnerDir == "" {
		return fmt.Errorf("tart_config.runner_dir is required")
	}
	return nil
}

// Backend implements BackendConfig.
func (cfg TartConfig) Backend(pool string) basepool.ProviderNew {
	return tartProviderNew{cfg: cfg, pool: pool}
}

// tartProviderNew creates the tart backend for one pool, see TartConfig. The
// pool's name is held here because the VMs of a pool are named after it, see
// vmNamePrefix, and nothing of the image is pulled or inspected until New is
// called.
type tartProviderNew struct {
	cfg  TartConfig
	pool string
}

var _ basepool.ProviderNew = tartProviderNew{}

// Name implements basepool.ProviderNew, and is the name the backend reports as
// a provider, see TartKind.
func (t tartProviderNew) Name() string { return TartKind }

// New implements basepool.ProviderNew.
func (t tartProviderNew) New(ctx context.Context) (basepool.Provider, error) {
	return newTartBackend(t.pool, t.cfg, loggerFor(ctx))
}

// VMSPrefix is the prefix shared by every VM name the orchestrator generates.
const VMSPrefix = "ghr-orchestrator-"

var vmInstID atomic.Int64

// vmNamePrefix returns the prefix shared by all of the VM names generated for
// the named pool and image. image should be the image name alone, as
// ociimage.Reference.Name reports it, since a VM name may not contain the "/"
// and ":" of a full reference.
func vmNamePrefix(pool, image string) string {
	return fmt.Sprintf("%s%s-%s-", VMSPrefix, pool, image)
}

// tartBackend is the tart backend for a pool. It reuses tartvm.Provider for the
// VM lifecycle (construction, listing, inspection and deletion via the tart
// CLI) and for everything it reports as a basepool.Provider: its name, the
// image it clones and the resources it creates VMs with.
type tartBackend struct {
	*tartvm.Provider
	cfg TartConfig
	// ref is cfg.Image parsed, which the pool's VM names are derived from and
	// which tart is given to clone.
	ref  ociimage.Reference
	name string
}

var _ basepool.Provider = (*tartBackend)(nil)

func newTartBackend(name string, cfg TartConfig, logger *slog.Logger) (*tartBackend, error) {
	ref, err := cfg.Reference()
	if err != nil {
		return nil, fmt.Errorf("vm_pool %v: tart_config.image: %w", name, err)
	}
	// The VM name is derived from the image name alone, since a VM name may
	// contain neither the "/" of a repository path nor the ":" of a tag; the
	// reference itself is passed to tart as the clone source and must keep its
	// registry and tag.
	prefix := vmNamePrefix(name, ref.Name)
	pull := cfg.ShouldPull(ref)
	opts := cfg.PullOptions()
	// The backend is allocated before its provider so that the constructor can
	// pull through it, see ociimage.Puller: the provider is built from the
	// constructor, so the two cannot be tied together in any other order. The
	// provider is set below, before the pool that would call the constructor
	// exists.
	backend := &tartBackend{cfg: cfg, ref: ref, name: name}
	constructor := func(ctx context.Context) (vms.Instance, error) {
		if pull {
			// Fail the creation rather than fall back to whatever is cached:
			// the point of pulling is that the VM runs the current image, and
			// silently using a stale one would defeat it.
			logger.Info("pulling image", "image", ref, "pool", name, "insecure", opts.Insecure, "concurrency", opts.Concurrency)
			if err := ociimage.Pull(ctx, backend.Provider, ref, opts); err != nil {
				logger.Error("failed to pull image", "image", ref, "pool", name, "err", err)
				return nil, fmt.Errorf("pulling %v for pool %v: %w", ref, name, err)
			}
		}
		vmName := fmt.Sprintf("%s%s-%04d", prefix, time.Now().Format("20060102-150405"), vmInstID.Add(1))
		tartOpts := slices.Clone(cfg.Options())
		tartOpts = append(tartOpts,
			tartvm.WithResources(cfg.VMResources),
			tartvm.WithLogger(logger),
			tartvm.WithObtainIPAtStart(false))
		return tartvm.New(ctx, ref.String(), vmName, tartOpts...), nil
	}
	backend.Provider = tartvm.NewProvider(constructor,
		tartvm.WithNamePrefix(prefix),
		tartvm.WithPoolName(name),
		// The constructor clones this image, so the provider reports it: it is
		// what a VM of this pool was actually created from, registry and tag
		// included.
		tartvm.WithProviderImage(ref),
		// The same resources the constructor creates VMs with, so that the
		// provider can report them for a VM that does not: tart reports
		// nothing for a VM it has not started yet.
		tartvm.WithProviderResources(cfg.VMResources),
		tartvm.WithProviderTartBinary(cfg.TartBinary))
	return backend, nil
}
