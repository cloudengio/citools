// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// TestRepoClientsRegistry covers the lookup keys and the reuse of an existing
// client: AddClient is called once per configured repository at startup and
// must not replace a client that other code already holds.
func TestRepoClientsRegistry(t *testing.T) {
	rc := NewRepoClients()

	first := rc.AddClient("cloudengio", "go.pkgs")
	if first == nil {
		t.Fatal("AddClient returned nil")
	}
	if again := rc.AddClient("cloudengio", "go.pkgs"); again != first {
		t.Error("AddClient replaced an existing client rather than returning it")
	}
	other := rc.AddClient("cloudengio", "go.macos")
	if other == first {
		t.Error("a different repository shares a client with the first")
	}

	// The same client is reachable by owner/repo and by full name.
	got, ok := rc.GetClient("cloudengio", "go.pkgs")
	if !ok || got != first {
		t.Errorf("GetClient: got %v, %v, want the registered client", got, ok)
	}
	got, ok = rc.GetClientFullName("cloudengio/go.pkgs")
	if !ok || got != first {
		t.Errorf("GetClientFullName: got %v, %v, want the registered client", got, ok)
	}

	// Unregistered repositories are reported as absent rather than created.
	if got, ok := rc.GetClient("cloudengio", "nope"); ok || got != nil {
		t.Errorf("GetClient(unregistered): got %v, %v, want nil, false", got, ok)
	}
	if got, ok := rc.GetClientFullName("nope/nope"); ok || got != nil {
		t.Errorf("GetClientFullName(unregistered): got %v, %v, want nil, false", got, ok)
	}
	// An owner/repo pair must not be confused with a full name.
	if _, ok := rc.GetClientFullName("cloudengio"); ok {
		t.Error("a bare owner matched a registered repository")
	}
}

// TestRepoClientsUnknownRepository verifies that every operation taking a full
// name reports an unregistered repository rather than dereferencing a nil
// client. None of these reach the network: the lookup fails first.
func TestRepoClientsUnknownRepository(t *testing.T) {
	rc := NewRepoClients()
	ctx := context.Background()
	const unknown = "nobody/nothing"

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"GetTokenFullName", func() error { _, err := rc.GetTokenFullName(ctx, unknown); return err }()},
		{"GetWorkflowJobFullName", func() error { _, err := rc.GetWorkflowJobFullName(ctx, unknown, 1); return err }()},
		{"RerunWorkflowJobFullName", rc.RerunWorkflowJobFullName(ctx, unknown, 1)},
		{"CancelWorkflowRunFullName", rc.CancelWorkflowRunFullName(ctx, unknown, 1)},
	} {
		if tc.err == nil {
			t.Errorf("%v: got nil error for an unregistered repository", tc.name)
			continue
		}
		if !strings.Contains(tc.err.Error(), unknown) {
			t.Errorf("%v: error %q does not name the repository", tc.name, tc.err)
		}
	}

	if _, err := rc.GetToken(ctx, "nobody", "nothing"); err == nil {
		t.Error("GetToken: got nil error for an unregistered repository")
	}
}

// TestRepoClientsConcurrent verifies that the registry is safe for the
// concurrent use it sees: webhook handling looks clients up from many
// goroutines. Run under -race to be meaningful.
func TestRepoClientsConcurrent(t *testing.T) {
	rc := NewRepoClients()
	const n = 50

	var wg sync.WaitGroup
	clients := make([]*Repo, n)
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			clients[i] = rc.AddClient("cloudengio", "go.pkgs")
		}()
	}
	wg.Wait()

	// Every caller must have been given the same client.
	for i, c := range clients {
		if c != clients[0] {
			t.Fatalf("goroutine %d got a different client", i)
		}
	}
}
