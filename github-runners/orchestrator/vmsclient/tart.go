// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
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
// the named pool and image. image should be a bare image name, as returned by
// bareImageName, since a VM name may not contain the "/" and ":" of a registry
// reference.
func vmNamePrefix(pool, image string) string {
	return fmt.Sprintf("%s%s-%s-", VMSPrefix, pool, image)
}

// bareImageName reduces an image reference to the image name alone, so that it
// can be used in a VM name. A reference may name a remote OCI registry, eg.
// "mm-1:5001/linux-ci:latest" or "ghcr.io/cirruslabs/macos-sequoia-base:latest",
// whose registry host and port, path and tag or digest are all stripped, leaving
// "linux-ci" and "macos-sequoia-base" respectively. A local image name is
// returned unchanged.
func bareImageName(image string) string {
	// A digest, if present, follows the whole reference.
	name, _, _ := strings.Cut(image, "@")
	// The registry host, its optional port, and any path segments precede the
	// image name; taking the last segment removes them all. A ":" surviving in
	// that segment can only introduce a tag, since a port appears only in the
	// first segment.
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	name, _, _ = strings.Cut(name, ":")
	return name
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
	// The VM name is derived from the image name alone; cfg.Image itself is
	// passed to tart as the clone source and must keep any registry and tag.
	prefix := vmNamePrefix(name, bareImageName(cfg.Image))
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
