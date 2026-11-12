// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cloudeng.io/vms/ociimage"
)

// TestBackendSelection covers what a pool configuration says about its
// backend before any pool is created, which is what validating a
// configuration file relies on.
func TestBackendSelection(t *testing.T) {
	ctx := context.Background()
	cfg := mockPool(1, MockConfig{Image: "fast-ci"})
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	// What the pool's VMs will be created by is known, and names the VM
	// technology, before anything is built for it.
	pn, err := cfg.backend("fast")
	if err != nil {
		t.Fatalf("backend: %v", err)
	}
	if got, want := pn.Name(), MockKind; got != want {
		t.Errorf("kind: got %v, want %v", got, want)
	}

	b, err := cfg.newBackend(ctx, "fast")
	if err != nil {
		t.Fatalf("newBackend: %v", err)
	}
	// The backend identifies itself and its image as any provider does, which
	// is what a VM created by it reports through its own provider. Its name is
	// the one the configuration promised, see basepool.ProviderNew.Name.
	if got, want := b.Name(), pn.Name(); got != want {
		t.Errorf("name: got %v, want %v", got, want)
	}
	if got, want := b.Details().Image().String(), "fast-ci"; got != want {
		t.Errorf("image: got %v, want %v", got, want)
	}
	// An unset image is reported as "mock" rather than empty, so that status
	// output identifies the pool.
	unnamed, err := mockPool(1, MockConfig{}).newBackend(ctx, "plain")
	if err != nil {
		t.Fatalf("newBackend: %v", err)
	}
	if got, want := unnamed.Details().Image().String(), DefaultMockImage; got != want {
		t.Errorf("image: got %v, want %v", got, want)
	}
	// Nothing is created from a mock pool's image, but it is still reported as
	// a reference, so one that is not a reference at all is rejected when the
	// configuration is read: a configuration written against a mock pool and
	// then pointed at a real backend fails now rather than only then.
	bad := mockPool(1, MockConfig{Image: "ghcr.io//fast-ci"})
	err = bad.Validate()
	if !errors.Is(err, ociimage.ErrInvalidReference) {
		t.Errorf("Validate: got %v, want ErrInvalidReference", err)
	}
	if err == nil || !strings.Contains(err.Error(), "mock_config.image") {
		t.Errorf("the error does not locate the setting: %v", err)
	}
	if _, err := bad.newBackend(ctx, "bad"); !errors.Is(err, ociimage.ErrInvalidReference) {
		t.Errorf("newBackend: got %v, want ErrInvalidReference", err)
	}

	// A configuration with no backend is rejected, by name.
	var none PoolConfig
	if err := none.Validate(); !errors.Is(err, ErrNoBackend) {
		t.Errorf("got %v, want ErrNoBackend", err)
	}
	// Nothing is reported for its backend either, by name, rather than a nil
	// one being asked what it is.
	if _, err := none.backend("none"); !errors.Is(err, ErrNoBackend) {
		t.Errorf("backend: got %v, want ErrNoBackend", err)
	}
	if _, err := none.newBackend(ctx, "none"); err == nil || !strings.Contains(err.Error(), "none") {
		t.Errorf("the error does not name the pool: %v", err)
	}
}

// TestListAndDeletePoolVMs covers what this package adds to basepool.ListVMs
// and basepool.DeleteVMs, which those are tested through: the configured
// backends reaching them, and a configuration that names no usable backend
// stopping the sweep rather than silently sweeping the pools that are
// configured properly.
func TestListAndDeletePoolVMs(t *testing.T) {
	ctx := context.Background()
	cfgs := map[string]PoolConfig{
		"one": mockPool(1, MockConfig{}),
		"two": mockPool(1, MockConfig{}),
	}
	// A mock backend owns no VMs outside the process, so there is nothing for
	// either to report.
	if vms, err := ListPoolVMs(ctx, cfgs); err != nil || len(vms) != 0 {
		t.Errorf("ListPoolVMs: got %v, %v, want none", vms, err)
	}
	if deleted, err := DeletePoolVMs(ctx, cfgs, time.Second); err != nil || len(deleted) != 0 {
		t.Errorf("DeletePoolVMs: got %v, %v, want none", deleted, err)
	}

	broken := map[string]PoolConfig{"one": cfgs["one"], "bad": {}}
	if _, err := ListPoolVMs(ctx, broken); !errors.Is(err, ErrNoBackend) {
		t.Errorf("ListPoolVMs: got %v, want ErrNoBackend", err)
	}
	if _, err := DeletePoolVMs(ctx, broken, time.Second); !errors.Is(err, ErrNoBackend) {
		t.Errorf("DeletePoolVMs: got %v, want ErrNoBackend", err)
	}
	// An unusable backend is named, so that the configuration file can be
	// corrected.
	if _, err := ListPoolVMs(ctx, broken); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Errorf("the error does not name the pool: %v", err)
	}
}
