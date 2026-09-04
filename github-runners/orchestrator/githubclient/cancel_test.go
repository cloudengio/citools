// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"context"
	"io"
	"testing"
	"time"

	"cloudeng.io/vms/vmspool"
	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
	gogithub "github.com/google/go-github/v89/github"
)

const (
	testOwner    = "cloudengio"
	testRepoName = "go.pkgs"
	testFullName = testOwner + "/" + testRepoName
	testPoolName = "mock"
)

// cancelHarness is a WorkflowEventHandler backed by a mock VM pool, with one
// live workflow instance holding a VM, as it would be while a job is running.
type cancelHarness struct {
	handler  *WorkflowEventHandler
	instance *WorkflowInstance
	vm       *vmspool.VM
	pools    *vmsclient.Pools
}

func newCancelHarness(t *testing.T) *cancelHarness {
	t.Helper()
	ctx := context.Background()
	t.Setenv("TMPDIR", t.TempDir())

	poolCfg := vmsclient.PoolConfig{Mock: &vmsclient.MockConfig{Image: "mock-ci"},
		Size:             2,
		StagingBehaviour: vmspool.StagingBehaviourRunning}
	poolCfg.CreateBackoff.InitialDelay = time.Millisecond
	poolCfg.CreateBackoff.Steps = 5
	pools, err := vmsclient.NewPools(ctx, map[string]vmsclient.PoolConfig{testPoolName: poolCfg},
		func(string) io.Writer { return io.Discard })
	if err != nil {
		t.Fatalf("NewPools: %v", err)
	}
	t.Cleanup(func() {
		if err := pools.Close(context.Background()); err != nil {
			t.Errorf("closing pools: %v", err)
		}
	})

	lm, err := internal.NewLogFileManager("cancel-test")
	if err != nil {
		t.Fatalf("NewLogFileManager: %v", err)
	}
	t.Cleanup(lm.CloseGlobalLogFile)

	h := &WorkflowEventHandler{
		runnerConfig:    map[string]RunnerConfig{},
		workflowManager: newWorkflowInstanceManager(lm),
		vmPools:         pools,
		poolConfigs:     map[string]vmsclient.PoolConfig{testPoolName: poolCfg},
		completeQueue:   NewCompletionQueue(ctx, 10, time.Minute, time.Minute),
		lm:              lm,
		clients:         NewRepoClients(),
		status:          newStatusTracker(time.Minute),
	}
	t.Cleanup(func() {
		if err := h.completeQueue.Close(context.Background()); err != nil {
			t.Errorf("closing the completion queue: %v", err)
		}
	})

	runner := &RunnerConfig{NamePrefix: "go-mock", VMPoolName: testPoolName,
		Labels: []string{"self-hosted", "mock"}, Timeout: time.Minute}
	inst := h.workflowManager.newInstance(ctx, runner, &poolCfg, jobEvent("queued", 0, ""))

	// Give the instance a VM, as AcquireVMAndToken would.
	vm, err := pools.Acquire(ctx, testPoolName)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	inst.vm = vm

	// The webhook carries the runner name, so the handler resolves the
	// instance without calling the GitHub API.
	h.status.upsert(inst.Name, func(rec *WorkflowSnapshot) {
		rec.State = WorkflowRunning
		rec.VMID = vm.ID()
	})
	return &cancelHarness{handler: h, instance: inst, vm: vm, pools: pools}
}

// jobEvent builds a workflow_job webhook payload.
func jobEvent(action string, jobID int64, runnerName string) *gogithub.WorkflowJobEvent {
	return &gogithub.WorkflowJobEvent{
		Action: new(action),
		WorkflowJob: new(gogithub.WorkflowJob{
			ID:         new(jobID),
			RunID:      new(int64(9999)),
			RunnerName: new(runnerName),
			Labels:     []string{"self-hosted", "mock"},
		}),
		Repo: new(gogithub.Repository{
			Name:     new(testRepoName),
			Owner:    &gogithub.User{Login: new(testOwner)},
			FullName: new(testFullName),
		}),
	}
}

// cancelEvent is what GitHub actually delivers when a run is cancelled: there
// is no "cancelled" action for workflow_job, only completed with a cancelled
// conclusion.
func cancelEvent(jobID int64, runnerName string) *gogithub.WorkflowJobEvent {
	e := jobEvent("completed", jobID, runnerName)
	e.WorkflowJob.Conclusion = new("cancelled")
	return e
}

func snapshotFor(t *testing.T, h *WorkflowEventHandler, name string) WorkflowSnapshot {
	t.Helper()
	for _, s := range h.Workflows() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no status record for %v", name)
	return WorkflowSnapshot{}
}

// vmState reports the state the pool has recorded for the named VM. The VM
// handle itself exposes no state, so the pool's status is the observable.
func vmState(t *testing.T, pools *vmsclient.Pools, id string) string {
	t.Helper()
	snaps, err := pools.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, s := range snaps {
		for _, vm := range s.VMs {
			if vm.Name == id {
				return vm.State
			}
		}
	}
	return ""
}

// TestCancelWebhookStopsRunningJob verifies the behaviour a cancellation
// depends on: a completed event that arrives while the job is still running
// locally is treated as a cancellation, the VM is stopped, and the record
// becomes canceled rather than completed.
func TestCancelWebhookStopsRunningJob(t *testing.T) {
	h := newCancelHarness(t)
	ctx := context.Background()

	// Sanity: the VM is running before the cancel arrives.
	if got, want := vmState(t, h.pools, h.vm.ID()), "Running"; got != want {
		t.Fatalf("VM state before cancel: got %q, want %q", got, want)
	}

	h.handler.handleCompleted(ctx, cancelEvent(1234, h.instance.Name))

	// The VM has been stopped, not left running.
	if got, want := vmState(t, h.pools, h.vm.ID()), "Stopped"; got != want {
		t.Errorf("VM state after cancel: got %q, want %q", got, want)
	}

	snap := snapshotFor(t, h.handler, h.instance.Name)
	if got, want := snap.State, WorkflowCanceled; got != want {
		t.Errorf("state: got %v, want %v", got, want)
	}
	if got, want := snap.Result, "cancelled"; got != want {
		t.Errorf("result: got %q, want %q", got, want)
	}
	if snap.Err == "" {
		t.Error("the record does not say why the job ended")
	}
	if snap.CompletedAt.IsZero() {
		t.Error("the record has no completion time")
	}

	// The instance is reported as a failure so that its VM is reclaimed.
	select {
	case ce := <-h.handler.completeQueue.Failure():
		if ce.Err == nil {
			t.Error("the completion event carries no error")
		}
		if got, want := ce.Payload.Name, h.instance.Name; got != want {
			t.Errorf("completion event for %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Error("no failure was queued for the cancelled job")
	}
}

// TestCompletedWebhookAfterJobFinished verifies the other path through the same
// handler: once the instance has been torn down, a completed event is an
// ordinary completion, not a cancellation.
func TestCompletedWebhookAfterJobFinished(t *testing.T) {
	h := newCancelHarness(t)
	ctx := context.Background()
	name := h.instance.Name

	// The job finished locally and its instance was removed.
	h.handler.workflowManager.deleteInstance(ctx, name)
	h.handler.status.upsert(name, func(rec *WorkflowSnapshot) {
		rec.State = WorkflowVMCompleted
	})

	e := jobEvent("completed", 1234, name)
	e.WorkflowJob.Conclusion = new("success")
	h.handler.handleCompleted(ctx, e)

	snap := snapshotFor(t, h.handler, name)
	if got, want := snap.State, WorkflowCompleted; got != want {
		t.Errorf("state: got %v, want %v", got, want)
	}
	if got, want := snap.Result, "success"; got != want {
		t.Errorf("result: got %q, want %q", got, want)
	}
}

// TestHandleWebhooksIgnoresUnknownAction verifies that actions the orchestrator
// does not handle are ignored rather than mishandled; GitHub sends "waiting"
// for jobs held by a deployment protection rule.
func TestHandleWebhooksIgnoresUnknownAction(t *testing.T) {
	h := newCancelHarness(t)
	if err := h.handler.HandleWebhooks(context.Background(), jobEvent("waiting", 1, h.instance.Name)); err != nil {
		t.Errorf("HandleWebhooks: %v", err)
	}
	// The running job is untouched.
	if got, want := vmState(t, h.pools, h.vm.ID()), "Running"; got != want {
		t.Errorf("VM state: got %q, want %q", got, want)
	}
}

// TestCompletedWebhookWhileJobStillRunningLocallyDoesNotTearDownVM verifies that
// a completed event with a normal conclusion (e.g. success) arriving while the job
// is still running locally does NOT tear down the VM or mark the job as canceled;
// the local runner is allowed to extract diagnostics and stop the VM cleanly.
func TestCompletedWebhookWhileJobStillRunningLocallyDoesNotTearDownVM(t *testing.T) {
	h := newCancelHarness(t)
	ctx := context.Background()

	// Sanity: VM is running.
	if got, want := vmState(t, h.pools, h.vm.ID()), "Running"; got != want {
		t.Fatalf("VM state before event: got %q, want %q", got, want)
	}

	e := jobEvent("completed", 1234, h.instance.Name)
	e.WorkflowJob.Conclusion = new("success")
	h.handler.handleCompleted(ctx, e)

	// The VM is NOT stopped: local execution is still in progress.
	if got, want := vmState(t, h.pools, h.vm.ID()), "Running"; got != want {
		t.Errorf("VM state after event: got %q, want %q", got, want)
	}

	snap := snapshotFor(t, h.handler, h.instance.Name)
	if got, want := snap.State, WorkflowRunning; got != want {
		t.Errorf("state: got %v, want %v", got, want)
	}
	if got, want := snap.Result, "success"; got != want {
		t.Errorf("result: got %q, want %q", got, want)
	}
}
