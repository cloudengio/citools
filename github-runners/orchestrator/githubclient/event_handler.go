// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"time"

	"cloudeng.io/errors"
	"cloudeng.io/logging/ctxlog"
	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
	gogithub "github.com/google/go-github/v89/github"
)

type WorkflowEventHandler struct {
	runnerConfig    map[string]RunnerConfig
	vmPools         *vmsclient.Pools
	poolConfigs     map[string]vmsclient.PoolConfig
	lm              *internal.LogFileManager
	completeQueue   *CompletionQueue
	workflowManager *workflowInstanceManager
	clients         *RepoClients
	status          *statusTracker
}

type CompletionQueue = vmsclient.CompletionQueue[WorkflowInstance]

func NewCompletionQueue(ctx context.Context, size int, successfulRetention, failedRetention time.Duration) *CompletionQueue {
	return vmsclient.NewCompletionQueue[WorkflowInstance](ctx, size, successfulRetention, failedRetention)
}

func NewWorkflowEventHandler(ctx context.Context, tmpDir string, cq *CompletionQueue, statusRetention time.Duration, poolConfigs map[string]vmsclient.PoolConfig, repoConfigs []RepositoryConfig, clients *RepoClients) (*WorkflowEventHandler, error) {
	lm, err := internal.NewLogFileManager(tmpDir)
	if err != nil {
		return nil, err
	}
	defer lm.CloseGlobalLogFile()
	vmPools, err := vmsclient.NewPools(ctx, poolConfigs, func(string) io.Writer {
		return lm.GlobalLogFile()
	})
	if err != nil {
		return nil, err
	}

	runnerMap := make(map[string]RunnerConfig, len(repoConfigs)*2)
	for _, rc := range repoConfigs {
		fullname := rc.Service.Owner + "/" + rc.Service.Repo
		for _, runner := range rc.Runners {
			labelKey := fullname + ":" + canonicalLabelSet(runner.Labels)
			runnerMap[labelKey] = runner
			ctxlog.Info(ctx, "handling workflow events", "repo", fullname, "runner_name_prefix", runner.NamePrefix, "label_key", labelKey)
		}
	}
	return &WorkflowEventHandler{
		runnerConfig:    runnerMap,
		workflowManager: newWorkflowInstanceManager(lm),
		vmPools:         vmPools,
		poolConfigs:     poolConfigs,
		completeQueue:   cq,
		lm:              lm,
		clients:         clients,
		status:          newStatusTracker(statusRetention),
	}, nil
}

// Workflows returns a snapshot of every running and recently-completed workflow
// job tracked by the orchestrator.
func (r *WorkflowEventHandler) Workflows() []WorkflowSnapshot {
	return r.status.list()
}

// Workflow returns the snapshot for a single workflow job by runner-instance
// name.
func (r *WorkflowEventHandler) Workflow(name string) (WorkflowSnapshot, bool) {
	return r.status.get(name)
}

// PoolStatus returns a snapshot of every configured VM pool and its VMs.
func (r *WorkflowEventHandler) PoolStatus(ctx context.Context) ([]vmsclient.PoolSnapshot, error) {
	return r.vmPools.Status(ctx)
}

// Subscribe returns a coalescing change signal that fires when either pool or
// workflow state changes, plus a cancel function that must be called to release
// both underlying subscriptions. The subscriptions are also released when ctx is
// cancelled.
func (r *WorkflowEventHandler) Subscribe(ctx context.Context) (<-chan struct{}, func()) {
	poolCh, poolCancel := r.vmPools.Subscribe(ctx)
	wfCh, wfCancel := r.status.subscribe(ctx)
	merged := make(chan struct{}, 1)
	done := make(chan struct{})
	notify := func() {
		select {
		case merged <- struct{}{}:
		default:
		}
	}
	go func() {
		for {
			select {
			case <-poolCh:
				notify()
			case <-wfCh:
				notify()
			case <-done:
				return
			}
		}
	}()
	cancel := func() {
		close(done)
		poolCancel()
		wfCancel()
	}
	return merged, cancel
}

func (r *WorkflowEventHandler) Close(ctx context.Context) error {
	r.lm.CloseGlobalLogFile() //nolint:errcheck // best effort cleanup of global log file.
	var errs errors.M
	err := r.vmPools.Close(ctx)
	if err != nil {
		ctxlog.Error(ctx, "failed to close VM pools", "error", err)
	} else {
		ctxlog.Info(ctx, "closed VM pools")
	}
	errs.Append(err)
	err = r.completeQueue.Close(ctx)
	if err != nil {
		ctxlog.Error(ctx, "failed to close completion queue", "error", err)
	} else {
		ctxlog.Info(ctx, "closed completion queue")
	}
	errs.Append(err)
	return errs.Err()
}

var (
	errUnsupportedAction = errors.New("unsupported action")
	errNoJob             = errors.New("workflow_job event has no workflow_job")
	errNoLabels          = errors.New("workflow_job event has no labels")
	errNoRepo            = errors.New("workflow_job event has no repository property")
	errNoRunnerConfig    = errors.New("workflow_job event has no corresponding local runner configuration")
	errNoVMPoolConfig    = errors.New("workflow_job event has no corresponding local VM pool configuration")
)

// matchOnLabelsAndAction checks if the event matches a runner configuration based on
// labels and action. Currently only queued actions are supported, if an
// action is not supported, the event is ignored and nil is returned along with a nil error.
func (r *WorkflowEventHandler) matchOnLabelsAndAction(ctx context.Context, event *gogithub.WorkflowJobEvent, actions []string) (*slog.Logger, *RunnerConfig, *vmsclient.PoolConfig, error) {
	logger := LoggerWithEvent(ctxlog.Logger(ctx), event)
	action := event.GetAction()
	if !slices.Contains(actions, action) {
		return logger, nil, nil, errUnsupportedAction
	}

	job := event.GetWorkflowJob()
	if job == nil {
		return logger, nil, nil, errNoJob
	}

	labels := job.GetLabels()
	if len(labels) == 0 {
		return logger, nil, nil, errNoLabels
	}

	wkflowRepo := event.GetRepo()
	if wkflowRepo == nil {
		return logger, nil, nil, errNoRepo
	}

	wkflowRepoName := wkflowRepo.GetFullName()
	labelKey := wkflowRepoName + ":" + canonicalLabelSet(labels)
	logger = logger.With("label_key", labelKey)
	runner, ok := r.runnerConfig[labelKey]
	if !ok {
		return logger, nil, nil, errNoRunnerConfig
	}
	pool, ok := r.poolConfigs[runner.VMPoolName]
	if !ok {
		return logger, nil, nil, errNoVMPoolConfig
	}
	logger = logger.With(LogRunnerConfigGroup(&runner))
	return logger, &runner, &pool, nil
}

func (r *WorkflowEventHandler) HandleWebhooks(ctx context.Context, event *gogithub.WorkflowJobEvent) error {
	supportedActions := []string{"queued", "in_progress", "completed"}
	logger, runner, pool, err := r.matchOnLabelsAndAction(ctx, event, supportedActions)
	if err != nil {
		if errors.Is(err, errUnsupportedAction) {
			logger.Info("workflow_job event ignored: action is not supported", "action", event.GetAction(), "supported_actions", supportedActions)
			return nil
		}
		logger.Error("workflow_job event ignored due to error", "err", err)
		return nil
	}
	ctx = ctxlog.WithLogger(ctx, logger)
	logger.Info("handling workflow_job event")
	switch event.GetAction() {
	case "queued":
		go r.handleQueuedEvent(ctx, event, pool, runner) //nolint:errcheck
		return nil
	case "in_progress":
		r.handleInProgressEvent(ctx, event)
		return nil
	case "completed":
		r.handleCompleted(ctx, event)
		return nil
	default:
		logger.Info("workflow_job event ignored: unsupported action", "action", event.GetAction())
		return nil
	}
}

// getInstanceForEvent resolves the runner-instance name for a workflow_job
// event (the name the orchestrator registered the runner under) and looks up the
// still-live instance for it. The returned name is valid even when the instance
// is no longer live (ok is false), so callers can still update the status
// tracker, which retains records past instance teardown.
func (r *WorkflowEventHandler) getInstanceForEvent(ctx context.Context, event *gogithub.WorkflowJobEvent) (*WorkflowInstance, string, bool) {
	job := event.GetWorkflowJob()
	if job == nil {
		ctxlog.Error(ctx, "workflow_job event has no workflow_job property")
		return nil, "", false
	}
	jobId := event.GetWorkflowJob().GetID()
	if jobId == 0 {
		ctxlog.Error(ctx, "workflow_job event has no workflow_job.id property")
		return nil, "", false
	}
	fullName := event.GetRepo().GetFullName()
	if len(fullName) == 0 {
		ctxlog.Error(ctx, "workflow_job event has no repository.full_name property")
		return nil, "", false
	}
	// Prefer the runner name carried on the webhook; fall back to the API for
	// events (e.g. queued) that don't populate it.
	runnerName := job.GetRunnerName()
	if runnerName == "" {
		njob, err := r.clients.GetWorkflowJobFullName(ctx, fullName, jobId)
		if err != nil {
			ctxlog.Error(ctx, "failed to get workflow job from GitHub", "job_id", jobId, "repo_full_name", fullName, "error", err)
			return nil, "", false
		}
		runnerName = njob.GetRunnerName()
	}
	inst, ok := r.workflowManager.getInstance(runnerName)
	return inst, runnerName, ok
}

func (r *WorkflowEventHandler) handleInProgressEvent(ctx context.Context, event *gogithub.WorkflowJobEvent) {
	inst, _, ok := r.getInstanceForEvent(ctx, event)
	if !ok {
		ctxlog.Error(ctx, "workflow_job event has no corresponding workflow instance")
		err := r.clients.RerunWorkflowJobFullName(ctx, event.GetRepo().GetFullName(), event.GetWorkflowJob().GetID())
		if err != nil {
			ctxlog.Error(ctx, "failed to rerun workflow job", "error", err)
		}
		return
	}
	logger := LoggerWithWorkflowInstance(ctxlog.Logger(ctx), inst)
	logger.Info("workflow_job in_progress event")
}

func (r *WorkflowEventHandler) handleCompleted(ctx context.Context, event *gogithub.WorkflowJobEvent) {
	inst, runnerName, ok := r.getInstanceForEvent(ctx, event)
	conclusion := event.GetWorkflowJob().GetConclusion()
	if !ok {
		// Normal path: the job already finished on the VM (state vm_completed)
		// and its instance was torn down; GitHub has now acknowledged completion.
		// Advance the retained status record to the terminal completed state.
		if runnerName != "" {
			r.status.upsert(runnerName, func(rec *WorkflowSnapshot) {
				rec.State = WorkflowCompleted
				rec.CompletedAt = time.Now()
				if conclusion != "" {
					rec.Result = conclusion
				}
			})
		}
		ctxlog.Info(ctx, "workflow_job completed on github", "conclusion", conclusion)
		return
	}
	// The instance is still live: GitHub reported completion while the job was
	// still running locally, i.e. it was canceled on GitHub. Tear the VM down.
	logger := LoggerWithWorkflowInstance(ctxlog.Logger(ctx), inst)
	logger.Info("workflow_job completed on github while still running locally, treating as canceled", "conclusion", conclusion)
	r.status.upsert(inst.Name, func(rec *WorkflowSnapshot) {
		rec.State = WorkflowCanceled
		rec.Err = "job canceled on github"
		rec.CompletedAt = time.Now()
		if conclusion != "" {
			rec.Result = conclusion
		}
	})
	vm := inst.GetVM()
	_, _ = vm.StopAndRelease(ctx, 30*time.Second)
	ce := vmsclient.CompletionEvent[WorkflowInstance]{Payload: *inst}
	err := fmt.Errorf("job canceled")
	r.completeQueue.PushFailure(ce, err)
}

func (r *WorkflowEventHandler) handleQueuedEvent(ctx context.Context, event *gogithub.WorkflowJobEvent, pool *vmsclient.PoolConfig, runner *RunnerConfig) error {

	ctx, cancel := context.WithTimeout(ctx, runner.Timeout)
	defer cancel()

	inst := r.workflowManager.newInstance(ctx, runner, pool, event)
	logger := LoggerWithWorkflowInstance(ctxlog.Logger(ctx), inst)
	ctx = ctxlog.WithLogger(ctx, logger)

	r.status.upsert(inst.Name, func(rec *WorkflowSnapshot) {
		snapshotFromInstance(inst)(rec)
		rec.State = WorkflowAcquiring
		rec.QueuedAt = time.Now()
	})

	if err := inst.AcquireVMAndToken(ctx, r.vmPools, r.clients); err != nil {
		ctxlog.Error(ctx, "failed to acquire VM and token", "error", err)
		r.status.upsert(inst.Name, func(rec *WorkflowSnapshot) {
			rec.State = WorkflowFailed
			rec.Err = err.Error()
			rec.CompletedAt = time.Now()
		})
		return err
	}

	defer r.workflowManager.deleteInstance(ctx, inst.Name)

	r.status.upsert(inst.Name, func(rec *WorkflowSnapshot) {
		snapshotFromInstance(inst)(rec)
		rec.State = WorkflowRunning
		rec.StartedAt = time.Now()
		if vm := inst.GetVM(); vm != nil {
			rec.VMID = vm.ID()
		}
	})

	runErr := inst.RunJob(ctx, r.completeQueue)
	// The VM has finished running the job locally at this point; record the
	// vm_completed state. GitHub's "completed" webhook (handleCompleted) will
	// later advance this to the completed state.
	r.status.upsert(inst.Name, func(rec *WorkflowSnapshot) {
		rec.State = WorkflowVMCompleted
		rec.VMCompletedAt = time.Now()
		if runErr != nil {
			rec.Result = "Failed"
			rec.Err = runErr.Error()
		} else {
			rec.Result = "Succeeded"
		}
	})
	return nil
}

func (r *WorkflowEventHandler) RunJob(ctx context.Context, owner, repo string, labels []string, waitForUserInput bool) error {
	wkflowRepoName := owner + "/" + repo
	labelKey := wkflowRepoName + ":" + canonicalLabelSet(labels)
	runner, ok := r.runnerConfig[labelKey]
	if !ok {
		return fmt.Errorf("no runner configuration found for labels %v, key %v", labels, labelKey)
	}

	event := &gogithub.WorkflowJobEvent{
		Action: gogithub.Ptr("queued"),
		WorkflowJob: gogithub.Ptr(gogithub.WorkflowJob{
			Name:         gogithub.Ptr(runner.NamePrefix + "-manual-run"),
			Labels:       labels,
			WorkflowName: gogithub.Ptr("manual-run"),
		}),
		Repo: gogithub.Ptr(gogithub.Repository{
			Name:     gogithub.Ptr(repo),
			Owner:    &gogithub.User{Login: gogithub.Ptr(owner)},
			FullName: gogithub.Ptr(wkflowRepoName),
			HTMLURL:  gogithub.Ptr(fmt.Sprintf("https://github.com/%s", wkflowRepoName)),
		}),
	}

	pool, ok := r.poolConfigs[runner.VMPoolName]
	if !ok {
		return fmt.Errorf("no pool configuration found for VM pool %s", runner.VMPoolName)
	}

	if err := r.handleQueuedEvent(ctx, event, &pool, &runner); err != nil {
		return fmt.Errorf("failed to handle queued event: %w", err)
	}
	r.DrainCompletionQueue(ctx, waitForUserInput)
	return nil
}

func (r *WorkflowEventHandler) DrainCompletionQueue(ctx context.Context, waitForInput bool) {
	for {
		select {
		case success := <-r.completeQueue.Success():
			inst := success.Payload
			logger := inst.GetLogger(ctxlog.Logger(ctx))
			logger.Info("job completed successfully")
			waitForUserInput(waitForInput, fmt.Sprintf("VM %s kept for debugging; press Enter to release it and continue...", inst.GetVM().ID()))
			inst.GetVM().Delete(ctx) //nolint:errcheck
		case failure := <-r.completeQueue.Failure():
			inst := failure.Payload
			logger := inst.GetLogger(ctxlog.Logger(ctx))
			logger.Error("job failed", "error", failure.Err)
			waitForUserInput(waitForInput, fmt.Sprintf("VM %s kept for debugging; press Enter to release it and continue...", inst.GetVM().ID()))
			inst.GetVM().Delete(ctx) //nolint:errcheck
		case <-ctx.Done():
			return
		default:
			return
		}
	}
}

func waitForUserInput(keepVM bool, prompt string) {
	if !keepVM {
		return
	}
	fmt.Fprint(os.Stderr, prompt)
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
