// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"cloudeng.io/vms/vmspool"
	"gopkg.in/yaml.v3"
)

func mockPool(size int, cfg MockConfig) PoolConfig {
	pc := PoolConfig{Mock: &cfg}
	pc.Size = size
	return pc
}

// discard is the log-file factory NewPools requires.
func discard(string) io.Writer { return io.Discard }

func TestMockProviderSelection(t *testing.T) {
	p, err := mockPool(1, MockConfig{RunnerDir: "/home/admin/actions-runner"}).newProvider("mock", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.Kind(), "mock"; got != want {
		t.Errorf("kind: got %q, want %q", got, want)
	}
	// An unset image is reported as "mock" rather than empty, so that status
	// output identifies the pool.
	if got, want := p.Image(), DefaultMockImage; got != want {
		t.Errorf("image: got %q, want %q", got, want)
	}
	if got, want := p.RunnerDir(), "/home/admin/actions-runner"; got != want {
		t.Errorf("runner dir: got %q, want %q", got, want)
	}

	named, err := mockPool(1, MockConfig{Image: "fast-ci"}).newProvider("mock", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := named.Image(), "fast-ci"; got != want {
		t.Errorf("image: got %q, want %q", got, want)
	}
}

// TestPoolConfigValidate covers the rule that a pool selects exactly one
// backend, which is what keeps the choice unambiguous as backends are added.
func TestPoolConfigValidate(t *testing.T) {
	tart := &TartConfig{Image: "macos-ci", RunnerDir: "/Users/admin/actions-runner"}
	for _, tc := range []struct {
		name string
		cfg  PoolConfig
		want error
	}{
		{"mock only", PoolConfig{Mock: &MockConfig{}}, nil},
		{"tart only", PoolConfig{Tart: tart}, nil},
		{"neither", PoolConfig{}, ErrNoBackend},
		{"both", PoolConfig{Tart: tart, Mock: &MockConfig{}}, ErrMultipleBackends},
	} {
		if err := tc.cfg.Validate(); !errors.Is(err, tc.want) {
			t.Errorf("%v: got %v, want %v", tc.name, err, tc.want)
		}
		// newProvider agrees with Validate about which configurations are usable.
		_, err := tc.cfg.newProvider(tc.name, slog.Default())
		if (err == nil) != (tc.want == nil) {
			t.Errorf("%v: newProvider got %v, Validate got %v", tc.name, err, tc.cfg.Validate())
		}
	}
}

// TestMockPoolsLifecycle drives real vmspool.Pools through the mock backend:
// the pools are started, fill to their configured sizes, hand out VMs and are
// closed, with no VM technology involved.
func TestMockPoolsLifecycle(t *testing.T) {
	ctx := context.Background()
	cfg := map[string]PoolConfig{
		"fast": mockPool(3, MockConfig{Image: "fast-ci"}),
		"slow": mockPool(1, MockConfig{Suspendable: true}),
	}
	for name, pc := range cfg {
		pc.StagingBehaviour = vmspool.StagingBehaviourRunning
		pc.CreateBackoff.InitialDelay = time.Millisecond
		pc.CreateBackoff.Steps = 5
		cfg[name] = pc
	}

	pools, err := NewPools(ctx, cfg, discard)
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer func() {
		if err := pools.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	// Each pool reports itself with its configured size and backend.
	snaps, err := pools.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got, want := len(snaps), 2; got != want {
		t.Fatalf("pools: got %d, want %d", got, want)
	}
	byName := map[string]PoolSnapshot{}
	for _, s := range snaps {
		byName[s.Name] = s
	}
	for name, wantSize := range map[string]int{"fast": 3, "slow": 1} {
		s, ok := byName[name]
		if !ok {
			t.Errorf("pool %v missing from the status", name)
			continue
		}
		if s.Size != wantSize {
			t.Errorf("pool %v: size %d, want %d", name, s.Size, wantSize)
		}
		if s.Kind != "mock" {
			t.Errorf("pool %v: kind %q, want mock", name, s.Kind)
		}
	}

	// A VM can be acquired and released, which is the whole point of a pool.
	vm, err := pools.Acquire(ctx, "fast")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if vm.ID() == "" {
		t.Error("the acquired VM has no ID")
	}
	// Commands are recorded rather than executed, so a mock VM reports success.
	if err := vm.Exec(ctx, io.Discard, io.Discard, "echo", "hello"); err != nil {
		t.Errorf("Exec on a mock VM: %v", err)
	}
	if err := vm.Delete(ctx); err != nil {
		t.Errorf("Delete: %v", err)
	}

	// An unconfigured pool is reported rather than panicking.
	if _, err := pools.Acquire(ctx, "nope"); err == nil {
		t.Error("acquiring from an unknown pool succeeded")
	}
}

// TestMockPoolYAML verifies that a mock pool can be configured entirely from
// YAML, which is what lets the orchestrator be run without VM technology.
func TestMockPoolYAML(t *testing.T) {
	const spec = `
size: 2
staging_behaviour: Suspended
mock_config:
  image: fast-ci
  runner_dir: /home/admin/actions-runner
  suspendable: true
`
	var pc PoolConfig
	if err := yaml.Unmarshal([]byte(spec), &pc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := pc.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if pc.Mock == nil {
		t.Fatal("mock_config was not decoded")
	}
	if got, want := pc.Size, 2; got != want {
		t.Errorf("size: got %d, want %d", got, want)
	}
	if !pc.Mock.Suspendable {
		t.Error("suspendable was not decoded")
	}
	if got, want := pc.RunnerDir(), "/home/admin/actions-runner"; got != want {
		t.Errorf("runner dir: got %q, want %q", got, want)
	}

	// A YAML pool with both backends is rejected.
	var both PoolConfig
	if err := yaml.Unmarshal([]byte("mock_config: {}\ntart_config:\n  image: x\n"), &both); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := both.Validate(); !errors.Is(err, ErrMultipleBackends) {
		t.Errorf("got %v, want ErrMultipleBackends", err)
	}
}

// TestMockPoolDeleteReportsNothing verifies that the mock backend owns no
// external VMs, so that `vms delete` cannot claim to have removed any.
func TestMockPoolDeleteReportsNothing(t *testing.T) {
	p, err := mockPool(1, MockConfig{}).newProvider("mock", slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := p.Delete(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("a mock pool reported deleting %v", strings.Join(deleted, ", "))
	}
}
