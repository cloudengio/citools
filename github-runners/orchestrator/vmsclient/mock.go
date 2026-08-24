// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"log/slog"

	"cloudeng.io/vms/vmstestutil"
)

// MockConfig configures the mock VM backend for a pool. The pool behaves
// exactly as a real one — it creates, stages, hands out and deletes VMs, and
// reports the same events — but the VMs are in-process fakes that run no
// commands. It exists so that the orchestrator can be exercised end to end,
// in tests and by hand, on a machine with no VM technology installed.
//
// Anything that would be run inside the VM is recorded rather than executed,
// so a mock pool reports success for any job.
type MockConfig struct {
	// Image is reported in status output in place of a real base image, so
	// that a mock pool is identifiable at a glance.
	Image string `yaml:"image" doc:"name reported in place of a base image for this pool; defaults to 'mock'"`
	// RunnerDir mirrors the tart backend's setting so that the runner
	// installation commands are built the same way.
	RunnerDir string `yaml:"runner_dir" doc:"directory on the guest in which the runner is installed; only recorded, never used"`
	// Suspendable selects whether the mock VMs support suspend and resume,
	// which determines whether a pool configured with the suspended staging
	// behaviour suspends its VMs or falls back to stopping them.
	Suspendable bool `yaml:"suspendable" doc:"whether the mock VMs support suspend/resume; when false a suspended pool falls back to stopping its VMs"`
}

// DefaultMockImage is reported by a mock pool that configures no image.
const DefaultMockImage = "mock"

// mockProvider is the mock backend for a pool. It reuses vmstestutil.MockFactory
// for the VM lifecycle and adds the orchestrator-specific metadata.
type mockProvider struct {
	*vmstestutil.MockFactory
	cfg  MockConfig
	name string
}

var _ Provider = (*mockProvider)(nil)

func newMockProvider(name string, cfg MockConfig, _ *slog.Logger) *mockProvider {
	return &mockProvider{
		MockFactory: vmstestutil.NewMockFactory(cfg.Suspendable),
		cfg:         cfg,
		name:        name,
	}
}

func (p *mockProvider) Kind() string { return "mock" }

func (p *mockProvider) Image() string {
	if p.cfg.Image == "" {
		return DefaultMockImage
	}
	return p.cfg.Image
}

func (p *mockProvider) RunnerDir() string { return p.cfg.RunnerDir }
