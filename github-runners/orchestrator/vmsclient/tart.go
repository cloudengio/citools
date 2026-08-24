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
)

// TartConfig configures the tart VM backend for a pool.
type TartConfig struct {
	tartvm.Config `yaml:",inline"`
	Image         string `yaml:"image" doc:"base image to use for cloning VMs in this pool"`
	RunnerDir     string `yaml:"runner_dir" doc:"directory on the VM in which the runner was installed, specific to each type of image."`
}

// VMSPrefix is the prefix shared by every VM name the orchestrator generates.
const VMSPrefix = "ghr-orchestrator-"

var vmInstID atomic.Int64

// vmNamePrefix returns the prefix shared by all of the VM names generated for
// the named pool and image.
func vmNamePrefix(pool, image string) string {
	return fmt.Sprintf("%s%s-%s-", VMSPrefix, pool, image)
}

// tartProvider is the tart backend for a pool. It reuses tartvm.Provider for the
// VM lifecycle (construction, listing, inspection and deletion via the tart CLI)
// and only adds the orchestrator-specific metadata.
type tartProvider struct {
	*tartvm.Provider
	cfg  TartConfig
	name string
}

var _ Provider = (*tartProvider)(nil)

func newTartProvider(name string, cfg TartConfig, logger *slog.Logger) *tartProvider {
	prefix := vmNamePrefix(name, cfg.Image)
	constructor := func(ctx context.Context) (vms.Instance, error) {
		vmName := fmt.Sprintf("%s%s-%04d", prefix, time.Now().Format("20060102-150405"), vmInstID.Add(1))
		opts := slices.Clone(cfg.Options())
		opts = append(opts, tartvm.WithLogger(logger), tartvm.WithObtainIPAtStart(false))
		return tartvm.New(ctx, cfg.Image, vmName, opts...), nil
	}
	return &tartProvider{
		Provider: tartvm.NewProvider(constructor,
			tartvm.WithNamePrefix(prefix),
			tartvm.WithPoolName(name),
			tartvm.WithProviderTartBinary(cfg.TartBinary)),
		cfg:  cfg,
		name: name,
	}
}

func (p *tartProvider) Kind() string      { return "tart" }
func (p *tartProvider) Image() string     { return p.cfg.Image }
func (p *tartProvider) RunnerDir() string { return p.cfg.RunnerDir }
