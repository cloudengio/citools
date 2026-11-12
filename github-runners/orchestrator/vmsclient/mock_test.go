// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"cloudeng.io/vms/ociimage"
	basepool "cloudeng.io/vms/vmspool"
	"gopkg.in/yaml.v3"
)

func mockPool(size int, cfg MockConfig) PoolConfig {
	pc := PoolConfig{Mock: &cfg}
	pc.Size = size
	pc.StagingBehaviour = basepool.StagingBehaviourRunning
	pc.CreateBackoff.InitialDelay = time.Millisecond
	pc.CreateBackoff.Steps = 5
	return pc
}

// discard is the log-file factory NewPools requires.
func discard(string) io.Writer { return io.Discard }

// TestMockBackendSelection covers the mock section of a pool configuration:
// the backend it selects, and the runner directory that the orchestrator
// reads back from the configuration rather than from the backend.
func TestMockBackendSelection(t *testing.T) {
	ctx := context.Background()
	cfg := mockPool(1, MockConfig{RunnerDir: "/home/admin/actions-runner"})
	b, err := cfg.newBackend(ctx, "mock")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := b.Name(), "mock"; got != want {
		t.Errorf("name: got %q, want %q", got, want)
	}
	// An unset image is reported as "mock" rather than empty, so that status
	// output identifies the pool.
	if got, want := b.Details().Image().String(), DefaultMockImage; got != want {
		t.Errorf("image: got %q, want %q", got, want)
	}
	if got, want := cfg.RunnerDir(), "/home/admin/actions-runner"; got != want {
		t.Errorf("runner dir: got %q, want %q", got, want)
	}

	named, err := mockPool(1, MockConfig{Image: "fast-ci"}).newBackend(ctx, "mock")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := named.Details().Image().String(), "fast-ci"; got != want {
		t.Errorf("image: got %q, want %q", got, want)
	}
}

// TestPoolConfigValidate covers the rule that a pool selects exactly one
// backend, which is what keeps the choice unambiguous as backends are added.
func TestPoolConfigValidate(t *testing.T) {
	tart := &TartConfig{
		Source:    ociimage.Source{Image: "macos-ci"},
		RunnerDir: "/Users/admin/actions-runner",
	}
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
		// Building the backend agrees with Validate about which
		// configurations are usable.
		_, err := tc.cfg.newBackend(context.Background(), tc.name)
		if (err == nil) != (tc.want == nil) {
			t.Errorf("%v: newBackend got %v, Validate got %v", tc.name, err, tc.cfg.Validate())
		}
	}

	// A tart pool must say what to clone and where the runner lives in it.
	for _, tc := range []struct {
		cfg  TartConfig
		want string
	}{
		{TartConfig{RunnerDir: "/runner"}, "tart_config.image is required"},
		{TartConfig{Source: ociimage.Source{Image: "macos-ci"}}, "tart_config.runner_dir is required"},
	} {
		err := PoolConfig{Tart: &tc.cfg}.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: got %v, want it to mention %q", tc.cfg, err, tc.want)
		}
	}
}

// TestMockPoolsLifecycle drives pools through the orchestrator's own
// configuration: a mock pool configured in a file becomes a working pool, with
// no VM technology involved.
func TestMockPoolsLifecycle(t *testing.T) {
	ctx := context.Background()
	pools, err := NewPools(ctx, map[string]PoolConfig{
		"fast": mockPool(2, MockConfig{Image: "fast-ci"}),
	}, discard)
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	defer func() {
		if err := pools.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		snaps, err := pools.Status(ctx)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if len(snaps) == 1 && snaps[0].Available == 2 {
			if got, want := snaps[0].Kind, "mock"; got != want {
				t.Errorf("kind: got %q, want %q", got, want)
			}
			if got, want := snaps[0].Image, "fast-ci"; got != want {
				t.Errorf("image: got %q, want %q", got, want)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the pool to fill: %+v", snaps)
		}
		time.Sleep(10 * time.Millisecond)
	}

	vm, err := pools.Acquire(ctx, "fast")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := vm.Delete(ctx); err != nil {
		t.Errorf("Delete: %v", err)
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
	if got, want := pc.Mock.Image, "fast-ci"; got != want {
		t.Errorf("image: got %q, want %q", got, want)
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

// TestTartPoolYAML verifies the configuration file surface of a tart pool,
// which is flat: the settings of tart itself, of the image, and of the VMs'
// resources are each defined by a different package and embedded inline, so
// a configuration file names them all together and knows nothing of the
// division.
func TestTartPoolYAML(t *testing.T) {
	const spec = `
size: 2
delete_acquired_on_close: true
tart_config:
  os: linux
  runner_dir: /home/admin/actions-runner
  image: mm-1:5001/linux-ci:latest
  pull_remote_on_run: true
  pull_insecure: true
  pull_concurrency: 4
  run_options: ["--no-graphics"]
  binary: /opt/homebrew/bin/tart
  num_cores: 8
  mem_gib: 16
`
	var pc PoolConfig
	if err := yaml.Unmarshal([]byte(spec), &pc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := pc.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if pc.Tart == nil {
		t.Fatal("tart_config was not decoded")
	}
	// The generic pool settings.
	if got, want := pc.Size, 2; got != want {
		t.Errorf("size: got %v, want %v", got, want)
	}
	// tart's own.
	if got, want := pc.Tart.OS, "linux"; got != want {
		t.Errorf("os: got %q, want %q", got, want)
	}
	if got, want := pc.Tart.TartBinary, "/opt/homebrew/bin/tart"; got != want {
		t.Errorf("binary: got %q, want %q", got, want)
	}
	// The image's, which ociimage.Source defines.
	if got, want := pc.Tart.Image, "mm-1:5001/linux-ci:latest"; got != want {
		t.Errorf("image: got %q, want %q", got, want)
	}
	if !pc.Tart.PullRemoteOnRun || !pc.Tart.Insecure {
		t.Errorf("the pull settings were not decoded: %+v", pc.Tart.Source)
	}
	if got, want := pc.Tart.Concurrency, 4; got != want {
		t.Errorf("pull_concurrency: got %v, want %v", got, want)
	}
	// The VMs' resources, which basepool.VMResources defines.
	if got, want := pc.Tart.NumCores, 8; got != want {
		t.Errorf("num_cores: got %v, want %v", got, want)
	}
	if got, want := pc.Tart.MemGiB, 16; got != want {
		t.Errorf("mem_gib: got %v, want %v", got, want)
	}
	// The reference is parsed from the image, so the pool's VMs are named
	// after the image alone whilst tart is given the whole of it.
	ref, err := pc.Tart.Reference()
	if err != nil {
		t.Fatalf("Reference: %v", err)
	}
	if got, want := ref.Name, "linux-ci"; got != want {
		t.Errorf("the VMs would be named after %q, want %q", got, want)
	}
	if !pc.Tart.ShouldPull(ref) {
		t.Error("a remote image with pull_remote_on_run set is not pulled")
	}
}

// TestMockPoolDeleteReportsNothing verifies that the mock backend owns no
// external VMs, so that `vms delete` cannot claim to have removed any.
func TestMockPoolDeleteReportsNothing(t *testing.T) {
	deleted, err := DeletePoolVMs(context.Background(),
		map[string]PoolConfig{"mock": mockPool(1, MockConfig{})}, time.Second)
	if err != nil {
		t.Fatalf("DeletePoolVMs: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("a mock pool reported deleting %v", strings.Join(deleted, ", "))
	}
}
