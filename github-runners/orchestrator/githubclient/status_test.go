// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"testing"
	"time"
)

func TestStatusTrackerLifecycle(t *testing.T) {
	tr := newStatusTracker(time.Hour)

	tr.upsert("w1", func(r *WorkflowSnapshot) {
		r.State = WorkflowAcquiring
		r.QueuedAt = time.Now()
		r.RepoFullName = "o/r"
	})
	got, ok := tr.get("w1")
	if !ok || got.State != WorkflowAcquiring || got.RepoFullName != "o/r" {
		t.Fatalf("after queue: %+v ok=%v", got, ok)
	}

	tr.upsert("w1", func(r *WorkflowSnapshot) {
		r.State = WorkflowRunning
		r.VMID = "vm1"
	})
	got, _ = tr.get("w1")
	if got.State != WorkflowRunning || got.VMID != "vm1" || got.RepoFullName != "o/r" {
		t.Fatalf("running upsert did not merge: %+v", got)
	}

	// Local VM completion precedes GitHub's acknowledgement.
	tr.upsert("w1", func(r *WorkflowSnapshot) {
		r.State = WorkflowVMCompleted
		r.Result = "Succeeded"
		r.VMCompletedAt = time.Now()
	})
	if got, _ := tr.get("w1"); got.State != WorkflowVMCompleted || got.VMCompletedAt.IsZero() {
		t.Fatalf("vm_completed not recorded: %+v", got)
	}

	tr.upsert("w1", func(r *WorkflowSnapshot) {
		r.State = WorkflowCompleted
		r.CompletedAt = time.Now()
	})
	if got, _ := tr.get("w1"); got.State != WorkflowCompleted || got.Result != "Succeeded" || got.CompletedAt.IsZero() {
		t.Fatalf("github completion not recorded: %+v", got)
	}

	if list := tr.list(); len(list) != 1 || list[0].Name != "w1" {
		t.Fatalf("list = %+v", list)
	}
}

func TestStatusTrackerPrunesStaleVMCompleted(t *testing.T) {
	// vm_completed is not terminal, but must still be pruned after the retention
	// window so records don't accumulate when GitHub never acknowledges.
	tr := newStatusTracker(10 * time.Millisecond)
	tr.upsert("stuck", func(r *WorkflowSnapshot) {
		r.State = WorkflowVMCompleted
		r.VMCompletedAt = time.Now().Add(-time.Hour)
	})
	tr.upsert("live", func(r *WorkflowSnapshot) { r.State = WorkflowRunning })
	tr.upsert("live", func(r *WorkflowSnapshot) { r.VMID = "vm9" }) // triggers prune
	if _, ok := tr.get("stuck"); ok {
		t.Error("stale vm_completed record was not pruned")
	}
	if isTerminal(WorkflowVMCompleted) {
		t.Error("vm_completed must not be terminal")
	}
}

func TestStatusTrackerRetentionPrunesTerminal(t *testing.T) {
	tr := newStatusTracker(10 * time.Millisecond)
	tr.upsert("done", func(r *WorkflowSnapshot) {
		r.State = WorkflowCompleted
		r.CompletedAt = time.Now().Add(-time.Hour) // long past the retention window
	})
	tr.upsert("live", func(r *WorkflowSnapshot) {
		r.State = WorkflowRunning
	})
	// The next upsert triggers a prune; the stale terminal record should go, the
	// running one must stay regardless of age.
	tr.upsert("live", func(r *WorkflowSnapshot) { r.VMID = "vm2" })

	if _, ok := tr.get("done"); ok {
		t.Error("expired terminal record was not pruned")
	}
	if _, ok := tr.get("live"); !ok {
		t.Error("running record was incorrectly pruned")
	}
}

func TestStatusTrackerNotifies(t *testing.T) {
	tr := newStatusTracker(time.Hour)
	ch, cancel := tr.bc.Subscribe()
	defer cancel()
	tr.upsert("w1", func(r *WorkflowSnapshot) { r.State = WorkflowQueued })
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected a change notification")
	}
}

func TestIsTerminal(t *testing.T) {
	terminal := []WorkflowState{WorkflowCompleted, WorkflowFailed, WorkflowCanceled}
	for _, s := range terminal {
		if !isTerminal(s) {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []WorkflowState{WorkflowQueued, WorkflowAcquiring, WorkflowRunning} {
		if isTerminal(s) {
			t.Errorf("%s should not be terminal", s)
		}
	}
}
