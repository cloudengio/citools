// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"cloudeng.io/vms/vmspool"
	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
)

// newQueuedHarness returns a handler for which acquisition cannot succeed: no
// client is registered for the repository, so GetTokenFullName fails without
// reaching GitHub. That is the failure handleQueuedEvent has to clean up
// after, and it is the one that leaves a VM to be discarded too, since the
// pool is a mock that hands one out.
func newQueuedHarness(t *testing.T) (*WorkflowEventHandler, *RunnerConfig, *vmsclient.PoolConfig) {
	t.Helper()
	ctx := context.Background()
	t.Setenv("TMPDIR", t.TempDir())

	poolCfg := vmsclient.PoolConfig{
		Mock:             &vmsclient.MockConfig{Image: "mock-ci"},
		Size:             2,
		StagingBehaviour: vmspool.StagingBehaviourRunning,
	}
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

	lm, err := internal.NewLogFileManager("queued-test")
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

	runner := &RunnerConfig{
		NamePrefix: "go-mock", VMPoolName: testPoolName,
		Labels: []string{"self-hosted", "mock"}, Timeout: time.Minute,
	}
	return h, runner, &poolCfg
}

// instanceCount reports how many workflow instances the manager is holding.
func (m *workflowInstanceManager) instanceCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.instances)
}

func TestHandleQueuedEventReleasesInstanceWhenAcquireFails(t *testing.T) {
	h, runner, poolCfg := newQueuedHarness(t)
	ctx := context.Background()

	err := h.handleQueuedEvent(ctx, jobEvent("queued", 1, ""), poolCfg, runner)
	if err == nil {
		t.Fatal("expected the acquisition to fail")
	}

	// The instance held a slot in the manager and two open log files from the
	// moment it was created, so a failure to acquire has to give them back.
	if got := h.workflowManager.instanceCount(); got != 0 {
		t.Errorf("the manager retained %v instances after a failed acquisition", got)
	}

	// The failure still has to be visible: the status record is separate from
	// the instance and outlives it, which is what makes releasing it safe.
	workflows := h.Workflows()
	if len(workflows) != 1 {
		t.Fatalf("got %v status records, want 1", len(workflows))
	}
	rec := workflows[0]
	if got, want := rec.State, WorkflowFailed; got != want {
		t.Errorf("got state %v, want %v", got, want)
	}
	if rec.Err == "" {
		t.Error("the status record does not say why it failed")
	}
	// The log paths are recorded as names, so the ui can still show them
	// after the files themselves have been closed.
	if rec.JobLogPath == "" || rec.DiagLogPath == "" {
		t.Errorf("got log paths %q and %q, want both recorded", rec.JobLogPath, rec.DiagLogPath)
	}
}

// openFDs returns how many file descriptors this process has open, or -1 when
// that cannot be determined.
func openFDs() int {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return -1
	}
	return len(entries)
}

func TestHandleQueuedEventDoesNotLeakOnRepeatedFailures(t *testing.T) {
	h, runner, poolCfg := newQueuedHarness(t)
	ctx := context.Background()

	// A few failures first, so that whatever the pool and the log manager
	// open once is already open and not counted as growth.
	for i := range 3 {
		if err := h.handleQueuedEvent(ctx, jobEvent("queued", int64(i), ""), poolCfg, runner); err == nil {
			t.Fatal("expected the acquisition to fail")
		}
	}

	before := openFDs()
	if before < 0 {
		t.Skip("the number of open file descriptors cannot be determined here")
	}

	// Every failure used to leave two log files open, so this used to grow by
	// twice the number of failures.
	const failures = 20
	for i := range failures {
		if err := h.handleQueuedEvent(ctx, jobEvent("queued", int64(100+i), ""), poolCfg, runner); err == nil {
			t.Fatal("expected the acquisition to fail")
		}
	}

	if after := openFDs(); after-before > failures/2 {
		t.Errorf("open file descriptors grew from %v to %v over %v failed acquisitions",
			before, after, failures)
	}
	if got := h.workflowManager.instanceCount(); got != 0 {
		t.Errorf("the manager retained %v instances after %v failed acquisitions", got, failures)
	}
}
