// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/sync/errgroup"
	"cloudeng.io/vms/vmspool"
	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
	gogithub "github.com/google/go-github/v89/github"
)

// WorkflowInstance represents a single instance of a workflow executed on a VM.
type WorkflowInstance struct {
	Name                              string
	RunStdoutStderr, DiagStdoutStderr io.Writer
	LogName, DiagName                 string
	RunnerConfig                      *RunnerConfig
	PoolConfig                        *vmsclient.PoolConfig
	Event                             *gogithub.WorkflowJobEvent
	RepoURL                           string
	vm                                *vmspool.VM
	token                             *gogithub.RegistrationToken
	logFile, diagLogs                 *os.File
}

var runnerNameId atomic.Int64

func newWorkflowInstance(ctx context.Context, lm *internal.LogFileManager, runnerConfig *RunnerConfig, poolConfig *vmsclient.PoolConfig, event *gogithub.WorkflowJobEvent) *WorkflowInstance {
	timestamp := time.Now().Format("20060102-150405")
	runnerName := fmt.Sprintf("%s-%s-%04d", strings.TrimSuffix(runnerConfig.NamePrefix, "-"), timestamp, runnerNameId.Add(1))
	wfi := &WorkflowInstance{
		Name:         runnerName,
		Event:        event,
		RunnerConfig: runnerConfig,
		PoolConfig:   poolConfig,
	}
	logFile, diagLogs, err := lm.CreateTempFilesForJob(runnerName)
	if err != nil {
		ctxlog.Error(ctx, "failed to create temp log files", "err", err)
		wfi.RunStdoutStderr = os.Stdout
		wfi.DiagStdoutStderr = io.Discard // don't write tar output to stderr, as it will be captured in the job log file
		wfi.LogName = "stdout/stderr"
		wfi.DiagName = "stdout/stderr"
		return wfi
	}
	wfi.RunStdoutStderr = io.MultiWriter(logFile, os.Stdout)
	wfi.DiagStdoutStderr = diagLogs
	wfi.logFile = logFile
	wfi.diagLogs = diagLogs
	wfi.LogName = logFile.Name()
	wfi.DiagName = diagLogs.Name()
	wfi.RepoURL = event.GetRepo().GetHTMLURL()
	return wfi
}

// GetVM implements the CompletionEventPayload interface, returning the VM associated with this workflow instance.
func (wi WorkflowInstance) GetVM() *vmspool.VM {
	return wi.vm
}

// GetLogger implements the CompletionEventPayload interface, returning a logger enriched with workflow instance details.
func (wi WorkflowInstance) GetLogger(logger *slog.Logger) *slog.Logger {
	logger = LoggerWithEvent(logger, wi.Event)
	logger = LoggerWithWorkflowInstance(logger, &wi)
	return logger
}

// AcquireVMAndToken acquires a VM and github registration token concurrently.
func (wi *WorkflowInstance) AcquireVMAndToken(ctx context.Context, pools *vmsclient.Pools, repoClients *RepoClients) error {
	var g errgroup.T

	poolName := wi.RunnerConfig.VMPoolName
	var token *gogithub.RegistrationToken
	var vm *vmspool.VM
	g.Go(func() error {
		var err error
		repoFullName := wi.Event.GetRepo().GetFullName()
		token, err = repoClients.GetTokenFullName(ctx, repoFullName)
		if err != nil {
			return fmt.Errorf("failed to get registration token for %s: %w", repoFullName, err)
		}
		return nil
	})

	g.Go(func() error {
		var err error
		vm, err = pools.Acquire(ctx, poolName)
		if err != nil {
			return fmt.Errorf("failed to acquire VM from pool %s: %w", poolName, err)
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		if vm != nil {
			vm.Delete(ctx) //nolint:errcheck // best effort cleanup of a VM that was never used.
		}
		return err
	}
	wi.vm = vm
	wi.token = token
	return nil

}

func (wi *WorkflowInstance) Close(ctx context.Context) {
	if wi.logFile != nil {
		if err := wi.logFile.Close(); err != nil {
			ctxlog.Error(ctx, "failed to close log file", "file", wi.logFile.Name(), "err", err)
		}
	}
	if wi.diagLogs != nil {
		if err := wi.diagLogs.Close(); err != nil {
			ctxlog.Error(ctx, "failed to close diag log file", "file", wi.diagLogs.Name(), "err", err)
		}
	}

}

// RunJob runs the job on the instance's VM and returns the local outcome (nil on
// success). The VM has finished running by the time this returns.
func (wi *WorkflowInstance) RunJob(ctx context.Context, cq *CompletionQueue) error {
	if wi.vm == nil || wi.token == nil {
		ctxlog.Error(ctx, "VM or token not initialized for workflow instance", "workflow_instance", wi.Name)
		return fmt.Errorf("VM or token not initialized for workflow instance %s", wi.Name)
	}
	shr := newSelfHostedRunner(wi, cq, wi.RepoURL, wi.token.GetToken())
	return shr.runQueuedJob(ctx, wi)
}

func newWorkflowInstanceManager(lm *internal.LogFileManager) *workflowInstanceManager {
	return &workflowInstanceManager{
		lm:        lm,
		instances: make(map[string]*WorkflowInstance),
	}
}

type workflowInstanceManager struct {
	mu        sync.Mutex
	lm        *internal.LogFileManager
	instances map[string]*WorkflowInstance
}

func (m *workflowInstanceManager) newInstance(ctx context.Context, runnerConfig *RunnerConfig, poolConfig *vmsclient.PoolConfig, event *gogithub.WorkflowJobEvent) *WorkflowInstance {
	instance := newWorkflowInstance(ctx, m.lm, runnerConfig, poolConfig, event)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instances[instance.Name] = instance
	return instance
}

func (m *workflowInstanceManager) getInstance(name string) (*WorkflowInstance, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	instance, ok := m.instances[name]
	return instance, ok
}

func (m *workflowInstanceManager) deleteInstance(ctx context.Context, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[name]
	if ok {
		inst.Close(ctx)
	}
	delete(m.instances, name)
}
