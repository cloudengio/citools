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

	"cloudeng.io/errors"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/sync/errgroup"
	"cloudeng.io/vms/vmspool"
	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
	gogithub "github.com/google/go-github/v89/github"
)

// WorkflowInstance represents a single instance of a workflow executed on a VM.
type WorkflowInstance struct {
	Name                        string
	RunStdoutStderr, DiagStdout io.Writer
	LogName, DiagName           string
	JobStarted                  *JobStartedInfo
	JobStartedRaw               []byte
	RunnerConfig                *RunnerConfig
	PoolConfig                  *vmsclient.PoolConfig
	Event                       *gogithub.WorkflowJobEvent
	RepoURL                     string
	vm                          *vmspool.VM
	token                       *gogithub.RegistrationToken
	logFile, diagLogs           *os.File
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
		wfi.DiagStdout = io.Discard // don't write tar output to stderr, as it will be captured in the job log file
		wfi.LogName = "stdout/stderr"
		wfi.DiagName = "stdout/stderr"
		wfi.RepoURL = event.GetRepo().GetHTMLURL()
		return wfi
	}
	wfi.RunStdoutStderr = io.MultiWriter(logFile, os.Stdout)
	wfi.DiagStdout = diagLogs
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
// success). The VM has finished running by the time this returns. It examines
// the jobStarted data reported by the VM's start hook, compares it with the
// workflow instance values (reporting errors to logs and UI if they differ), and
// ensures that the job status on GitHub is completed or canceled.
func (wi *WorkflowInstance) RunJob(ctx context.Context, cq *CompletionQueue, clients *RepoClients, status *statusTracker) error {
	if wi.vm == nil || wi.token == nil {
		ctxlog.Error(ctx, "VM or token not initialized for workflow instance", "workflow_instance", wi.Name)
		return fmt.Errorf("VM or token not initialized for workflow instance %s", wi.Name)
	}
	shr := newSelfHostedRunner(wi, cq, wi.RepoURL, wi.token.GetToken())
	jobStarted, runErr := shr.runQueuedJob(ctx, wi)

	var errs errors.M
	errs.Append(runErr)

	// a) Compare jobStarted values with values in wi based on guaranteed identical fields:
	// Run ID, Run Attempt, Repository Name, Repository Owner, Triggering Actor,
	// Workflow Name, Commit SHA, and Git Ref.
	// Note: Job name is NOT checked as $GITHUB_JOB (YAML ID) can differ from workflow_job.name.
	if jobStarted != nil {
		var diffs []string

		// 1. Run ID: $GITHUB_RUN_ID vs .workflow_job.run_id
		expectedRunID := int64(0)
		if wi.Event != nil && wi.Event.GetWorkflowJob() != nil {
			expectedRunID = wi.Event.GetWorkflowJob().GetRunID()
		}
		if jobStarted.RunID != 0 && expectedRunID != 0 && jobStarted.RunID != expectedRunID {
			diffs = append(diffs, fmt.Sprintf("run_id mismatch: assigned=%d expected=%d", jobStarted.RunID, expectedRunID))
		}

		// 2. Run Attempt: $GITHUB_RUN_ATTEMPT vs .workflow_job.run_attempt
		expectedRunAttempt := int64(0)
		if wi.Event != nil && wi.Event.GetWorkflowJob() != nil {
			expectedRunAttempt = wi.Event.GetWorkflowJob().GetRunAttempt()
		}
		if jobStarted.RunAttempt != 0 && expectedRunAttempt != 0 && jobStarted.RunAttempt != expectedRunAttempt {
			diffs = append(diffs, fmt.Sprintf("run_attempt mismatch: assigned=%d expected=%d", jobStarted.RunAttempt, expectedRunAttempt))
		}

		// 3. Repository Name: $GITHUB_REPOSITORY vs .repository.full_name
		expectedRepo := ""
		if wi.Event != nil && wi.Event.GetRepo() != nil {
			expectedRepo = wi.Event.GetRepo().GetFullName()
		}
		if jobStarted.Repository != "" && expectedRepo != "" && !strings.EqualFold(jobStarted.Repository, expectedRepo) {
			diffs = append(diffs, fmt.Sprintf("repository mismatch: assigned=%q expected=%q", jobStarted.Repository, expectedRepo))
		}

		// 4. Repository Owner: $GITHUB_REPOSITORY_OWNER vs .repository.owner.login
		expectedOwner := ""
		if wi.Event != nil && wi.Event.GetRepo() != nil && wi.Event.GetRepo().GetOwner() != nil {
			expectedOwner = wi.Event.GetRepo().GetOwner().GetLogin()
		}
		if jobStarted.RepositoryOwner != "" && expectedOwner != "" && !strings.EqualFold(jobStarted.RepositoryOwner, expectedOwner) {
			diffs = append(diffs, fmt.Sprintf("repository_owner mismatch: assigned=%q expected=%q", jobStarted.RepositoryOwner, expectedOwner))
		}

		// 5. Triggering Actor: $GITHUB_ACTOR vs .sender.login
		expectedActor := ""
		if wi.Event != nil && wi.Event.GetSender() != nil {
			expectedActor = wi.Event.GetSender().GetLogin()
		}
		if jobStarted.Actor != "" && expectedActor != "" && !strings.EqualFold(jobStarted.Actor, expectedActor) {
			diffs = append(diffs, fmt.Sprintf("actor mismatch: assigned=%q expected=%q", jobStarted.Actor, expectedActor))
		}

		// 6. Workflow Name: $GITHUB_WORKFLOW vs .workflow_job.workflow_name
		expectedWorkflow := ""
		if wi.Event != nil && wi.Event.GetWorkflowJob() != nil {
			expectedWorkflow = wi.Event.GetWorkflowJob().GetWorkflowName()
		}
		if jobStarted.Workflow != "" && expectedWorkflow != "" && jobStarted.Workflow != expectedWorkflow {
			diffs = append(diffs, fmt.Sprintf("workflow mismatch: assigned=%q expected=%q", jobStarted.Workflow, expectedWorkflow))
		}

		// 7. Commit SHA: $GITHUB_SHA vs .workflow_job.head_sha
		expectedSHA := ""
		if wi.Event != nil && wi.Event.GetWorkflowJob() != nil {
			expectedSHA = wi.Event.GetWorkflowJob().GetHeadSHA()
		}
		if jobStarted.SHA != "" && expectedSHA != "" && !strings.EqualFold(jobStarted.SHA, expectedSHA) {
			diffs = append(diffs, fmt.Sprintf("sha mismatch: assigned=%q expected=%q", jobStarted.SHA, expectedSHA))
		}

		// 8. Git Ref: $GITHUB_REF vs .workflow_job.head_branch (e.g. refs/heads/main vs main)
		expectedBranch := ""
		if wi.Event != nil && wi.Event.GetWorkflowJob() != nil {
			expectedBranch = wi.Event.GetWorkflowJob().GetHeadBranch()
		}
		if jobStarted.Ref != "" && expectedBranch != "" {
			refClean := strings.TrimPrefix(strings.TrimPrefix(jobStarted.Ref, "refs/heads/"), "refs/tags/")
			branchClean := strings.TrimPrefix(strings.TrimPrefix(expectedBranch, "refs/heads/"), "refs/tags/")
			if refClean != branchClean && jobStarted.Ref != expectedBranch {
				diffs = append(diffs, fmt.Sprintf("ref mismatch: assigned=%q expected=%q", jobStarted.Ref, expectedBranch))
			}
		}

		if len(diffs) > 0 {
			diffMsg := fmt.Sprintf("assigned workflow run differs from queued event: %s", strings.Join(diffs, "; "))
			ctxlog.Error(ctx, diffMsg, "workflow_instance", wi.Name,
				"assigned_run_id", jobStarted.RunID, "expected_run_id", expectedRunID,
				"assigned_repo", jobStarted.Repository, "expected_repo", expectedRepo)
			if wi.RunStdoutStderr != nil {
				fmt.Fprintf(wi.RunStdoutStderr, "\nERROR: %s\n", diffMsg)
			}
			if status != nil {
				status.upsert(wi.Name, func(rec *WorkflowSnapshot) {
					rec.Err = diffMsg
					rec.Result = "Failed"
				})
			}
			errs.Append(fmt.Errorf("%s", diffMsg))
		}
	}

	// b) Regardless of any error from runQueuedJob, ensure record status on GitHub is completed or canceled
	if clients != nil {
		targetRepo := ""
		if jobStarted != nil && jobStarted.Repository != "" {
			targetRepo = jobStarted.Repository
		} else if wi.Event != nil && wi.Event.GetRepo() != nil {
			targetRepo = wi.Event.GetRepo().GetFullName()
		}

		targetRunID := int64(0)
		if jobStarted != nil && jobStarted.RunID != 0 {
			targetRunID = jobStarted.RunID
		} else if wi.Event != nil && wi.Event.GetWorkflowJob() != nil {
			targetRunID = wi.Event.GetWorkflowJob().GetRunID()
		}

		targetJobName := ""
		if jobStarted != nil && jobStarted.Job != "" {
			targetJobName = jobStarted.Job
		} else if wi.Event != nil && wi.Event.GetWorkflowJob() != nil {
			targetJobName = wi.Event.GetWorkflowJob().GetName()
		}

		targetJobID := int64(0)
		if wi.Event != nil && wi.Event.GetWorkflowJob() != nil {
			if jobStarted == nil || jobStarted.RunID == 0 || jobStarted.RunID == wi.Event.GetWorkflowJob().GetRunID() {
				targetJobID = wi.Event.GetWorkflowJob().GetID()
			}
		}

		if targetRepo != "" && (targetRunID != 0 || targetJobID != 0) {
			if ghErr := clients.EnsureJobCompletedOrCanceled(ctx, targetRepo, targetRunID, targetJobID, targetJobName); ghErr != nil {
				ctxlog.Error(ctx, "failed to ensure job completed or canceled on GitHub",
					"workflow_instance", wi.Name,
					"repo", targetRepo,
					"run_id", targetRunID,
					"job_id", targetJobID,
					"error", ghErr,
				)
				if wi.RunStdoutStderr != nil {
					fmt.Fprintf(wi.RunStdoutStderr, "\nERROR: failed to ensure job completed/canceled on GitHub: %v\n", ghErr)
				}
				if status != nil {
					status.upsert(wi.Name, func(rec *WorkflowSnapshot) {
						if rec.Err == "" {
							rec.Err = ghErr.Error()
						}
					})
				}
				errs.Append(ghErr)
			}
		}
	}

	return errs.Err()
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
