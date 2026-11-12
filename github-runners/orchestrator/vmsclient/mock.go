// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"fmt"

	"cloudeng.io/vms/ociimage"
	basepool "cloudeng.io/vms/vmspool"
	"cloudeng.io/vms/vmstestutil"
)

// DefaultMockImage is reported by a mock pool that configures no image, so
// that status output has something to show.
const DefaultMockImage = "mock"

// MockConfig configures the mock VM backend. A pool backed by it behaves
// exactly as a real one — it creates, stages, hands out and deletes VMs, and
// reports the same events — but the VMs are in-process fakes that run no
// commands: anything that would be run inside one is recorded rather than
// executed, so a mock VM reports success for anything.
//
// It exists so that the orchestrator can be exercised end to end, in tests and
// by hand, on a machine with no VM technology installed at all. It is also the
// backend this package's own tests use.
type MockConfig struct {
	// Image is reported in status output in place of a real base image, so
	// that a mock pool is identifiable at a glance. Nothing is created from
	// it, but it must still be a reference, see ociimage.Parse.
	Image string `yaml:"image" doc:"image reference reported in place of a base image for this pool; nothing is created from it, but it must still parse as one. Defaults to 'mock'"`
	// Suspendable selects whether the mock VMs support suspend and resume,
	// which determines whether a pool configured with the suspended staging
	// behaviour suspends its VMs or falls back to stopping them.
	Suspendable bool `yaml:"suspendable" doc:"whether the mock VMs support suspend/resume; when false a suspended pool falls back to stopping its VMs"`
	// RunnerDir mirrors the tart backend's setting so that the runner
	// installation commands are built the same way.
	RunnerDir string `yaml:"runner_dir" doc:"directory on the guest in which the runner is installed; only recorded, never used"`
}

// MockKind names the mock VM technology, as both the configuration that
// selects it and the backend that implements it report it. It is
// vmstestutil's own name for itself, so that the two cannot drift apart.
const MockKind = vmstestutil.MockProviderName

// Validate implements BackendConfig. A mock pool needs nothing of its own:
// its VMs are created in process, from no image at all. The image it reports
// in their place must still be a reference, so that a configuration written
// against a mock pool and then pointed at a real backend does not fail only
// then.
func (cfg MockConfig) Validate() error {
	if _, err := cfg.reference(); err != nil {
		return fmt.Errorf("mock_config.image: %w", err)
	}
	return nil
}

// reference is the image the pool reports, which is DefaultMockImage for a
// configuration that names none.
func (cfg MockConfig) reference() (ociimage.Reference, error) {
	if cfg.Image == "" {
		return ociimage.Reference{Name: DefaultMockImage}, nil
	}
	return ociimage.Parse(cfg.Image)
}

// Backend implements BackendConfig.
func (cfg MockConfig) Backend(pool string) basepool.ProviderNew {
	return mockProviderNew{cfg: cfg, pool: pool}
}

// mockProviderNew creates the mock backend for one pool, see MockConfig.
type mockProviderNew struct {
	cfg  MockConfig
	pool string
}

var _ basepool.ProviderNew = mockProviderNew{}

// Name implements basepool.ProviderNew, and is the name the backend reports as
// a provider, see MockKind.
func (m mockProviderNew) Name() string { return MockKind }

// New implements basepool.ProviderNew.
func (m mockProviderNew) New(context.Context) (basepool.Provider, error) {
	factory := vmstestutil.NewMockFactory(m.cfg.Suspendable)
	// The factory reports the image as its own, which is how the pool and
	// anything reporting on one of its VMs find it.
	image, err := m.cfg.reference()
	if err != nil {
		return nil, fmt.Errorf("vm_pool %v: mock_config.image: %w", m.pool, err)
	}
	factory.SetImage(image)
	return &mockBackend{
		MockFactory: factory,
		cfg:         m.cfg,
		name:        m.pool,
	}, nil
}

// mockBackend is the mock backend for a pool. It is vmstestutil.MockFactory
// in full: the VM lifecycle, and the name, image and resources it reports as a
// provider, see vmstestutil.DefaultMockResources for the figures a mock VM
// stands in with.
type mockBackend struct {
	*vmstestutil.MockFactory
	cfg  MockConfig
	name string
}

var _ basepool.Provider = (*mockBackend)(nil)
