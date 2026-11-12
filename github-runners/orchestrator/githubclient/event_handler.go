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
	"cloudeng.io/vms/vmspool"
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

// CompletionQueue parks finished jobs, and the VMs they ran on, so that a VM
// can be inspected before it is deleted, see vmspool.CompletionQueue.
type CompletionQueue = vmspool.CompletionQueue

// The outcomes the orchestrator files finished jobs under. A job that failed
// is worth keeping for longer than one that succeeded, which is why they are
// separate queues with retention periods of their own.
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
)

// CompletionOutcomes lists the outcomes configured for finished jobs.
var CompletionOutcomes = []string{OutcomeSuccess, OutcomeFailure}

// WithCompletionQueue configures the queue the orchestrator parks finished
// jobs in: one outcome for a job that succeeded and one for a job that failed,
// each holding at most size of them for its own retention period.
//
// It is an option for the VM pools because they are what creates the queue and
// closes it: every VM they hand out carries it, which is how a finished job
// reaches it, and closing them releases the VMs it is still holding. The
// handler fills and drains it, see vmsclient.Pools.CompletionQueue; a pool has
// no notion of a job being finished.
func WithCompletionQueue(size int, successfulRetention, failedRetention time.Duration) vmsclient.Option {
	return vmsclient.WithCompletionOutcomes(
		vmspool.WithCompletionOutcome(OutcomeSuccess, size, successfulRetention),
		vmspool.WithCompletionOutcome(OutcomeFailure, size, failedRetention))
}

// outcomeFor returns the outcome a finished job is filed under.
func outcomeFor(err error) string {
	if err != nil {
		return OutcomeFailure
	}
	return OutcomeSuccess
}

// pushCompletion parks a finished job in the completion queue of the pool its
// VM came from, see vmspool.VM.CompletionQueue, with err reporting how the job
// ended. The VM is all the queue is reached through, so nothing has to be
// handed the queue alongside it.
//
// A job that ran on no VM has nothing to park. One whose pool has no queue
// attached is deleted here instead: it has been stopped and released, and
// nothing else would ever delete it.
func pushCompletion(ctx context.Context, inst *WorkflowInstance, err error) {
	vm := inst.GetVM()
	if vm == nil {
		return
	}
	if cq := vm.CompletionQueue(); cq != nil {
		cq.Push(vmspool.CompletionEvent{VM: inst, Err: err, Outcome: outcomeFor(err)})
		return
	}
	if delErr := vm.Delete(ctx); delErr != nil {
		ctxlog.Error(ctx, "failed to delete the VM of a finished job with no completion queue",
			"vm", vm.ID(), "error", delErr)
	}
}

// NewWorkflowEventHandler creates the handler, and with it the VM pools the
// jobs it handles run on. poolOpts configures those pools; the orchestrator
// passes WithCompletionQueue, so that the VMs of finished jobs are parked
// rather than deleted at once, and a handler created without it deletes each
// VM as the job on it finishes.
func NewWorkflowEventHandler(ctx context.Context, tmpDir string, statusRetention time.Duration, poolConfigs map[string]vmsclient.PoolConfig, repoConfigs []RepositoryConfig, clients *RepoClients, poolOpts ...vmsclient.Option) (*WorkflowEventHandler, error) {
	lm, err := internal.NewLogFileManager(tmpDir)
	if err != nil {
		return nil, err
	}
	defer lm.CloseGlobalLogFile()
	vmPools, err := vmsclient.NewPools(ctx, poolConfigs, func(string) io.Writer {
		return lm.GlobalLogFile()
	}, poolOpts...)
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
		// The pools own the queue, so the handler takes it from them rather
		// than being handed one: it fills and drains it, and they close it.
		completeQueue: vmPools.CompletionQueue(),
		lm:            lm,
		clients:       clients,
		status:        newStatusTracker(statusRetention),
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

// ErrWorkflowNotRunning is returned by Cancel when no live workflow with the
// given name exists (it may already have completed).
var ErrWorkflowNotRunning = errors.New("no such running workflow")

// Cancel cancels a running workflow job by runner-instance name. It cancels the
// job's GitHub workflow run; the resulting "completed" webhook then tears the
// job's VM down through the normal completion path (see handleCompleted). It
// returns ErrWorkflowNotRunning if no live instance exists for name.
func (r *WorkflowEventHandler) Cancel(ctx context.Context, name string) error {
	inst, ok := r.workflowManager.getInstance(name)
	if !ok {
		return ErrWorkflowNotRunning
	}
	event := inst.Event
	fullName := event.GetRepo().GetFullName()
	runID := event.GetWorkflowJob().GetRunID()
	if fullName == "" || runID == 0 {
		return fmt.Errorf("workflow %q has no cancellable GitHub run", name)
	}
	if err := r.clients.CancelWorkflowRunFullName(ctx, fullName, runID); err != nil {
		return fmt.Errorf("failed to cancel GitHub run %d for %s: %w", runID, fullName, err)
	}
	// Reflect the request immediately; the "completed" webhook will move the
	// record to canceled and tear the VM down.
	r.status.upsert(name, func(rec *WorkflowSnapshot) {
		rec.State = WorkflowCanceled
		if rec.Err == "" {
			rec.Err = "cancellation requested"
		}
	})
	ctxlog.Info(ctx, "workflow cancellation requested", "name", name, "repo", fullName, "run_id", runID)
	return nil
}

// PoolStatus returns a snapshot of every configured VM pool and its VMs.
func (r *WorkflowEventHandler) PoolStatus(ctx context.Context) ([]vmsclient.PoolSnapshot, error) {
	return r.vmPools.Status(ctx)
}

// HasCapacity reports whether the VM pools can supply a VM for a queued job,
// see vmsclient.Pools.HasCapacity. It is what gates the webhook listener's
// connection to the relay: there is no point in holding it, or in reading
// events that would only queue up here, whilst no job can be started.
//
// A handler with no pools has no capacity, since a job has nothing to run on,
// which leaves the events at the relay for an orchestrator that does.
func (r *WorkflowEventHandler) HasCapacity() bool {
	if r.vmPools == nil {
		return false
	}
	return r.vmPools.HasCapacity()
}

// RefreshPool removes all currently available VMs in the named pool and replaces
// them with newly cloned ones. Acquired (busy) VMs are not affected.
func (r *WorkflowEventHandler) RefreshPool(ctx context.Context, name string) (int, error) {
	if r.vmPools == nil {
		return 0, fmt.Errorf("no VM pools configured")
	}
	return r.vmPools.Refresh(ctx, name)
}

// CompletionQueueVMIDs returns the set of VM IDs currently in the completion
// queue, as a set so that the ui can ask about each VM it is listing.
func (r *WorkflowEventHandler) CompletionQueueVMIDs() map[string]bool {
	if r.completeQueue == nil {
		return nil
	}
	ids := r.completeQueue.VMIDs()
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// Subscribe returns a coalescing change signal that fires when either pool,
// workflow, or completion queue state changes, plus a cancel function that must be called to release
// all underlying subscriptions. The subscriptions are also released when ctx is
// cancelled.
func (r *WorkflowEventHandler) Subscribe(ctx context.Context) (<-chan struct{}, func()) {
	poolCh, poolCancel := r.vmPools.Subscribe(ctx)
	wfCh, wfCancel := r.status.subscribe(ctx)
	var cqCh <-chan struct{}
	var cqCancel func()
	if r.completeQueue != nil {
		cqCh, cqCancel = r.completeQueue.Subscribe(ctx)
	}
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
			case <-cqCh:
				notify()
			case <-done:
				return
			}
		}
	}()
	cancel := func() {
		close(done)
		if poolCancel != nil {
			poolCancel()
		}
		if wfCancel != nil {
			wfCancel()
		}
		if cqCancel != nil {
			cqCancel()
		}
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
	// The completion queue is not closed here: it belongs to the pools, which
	// created it and have just closed it, releasing the VMs it was holding,
	// see vmsclient.Pools.Close.
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
	// If the job completed on GitHub with any conclusion other than "cancelled"
	// (e.g. "success", "failure"), it completed normally.
	// runQueuedJob is currently finishing locally (extracting logs, stopping VM).
	// Do NOT treat normal completions as cancellations or tear down the VM here!
	if conclusion != "cancelled" {
		r.status.upsert(inst.Name, func(rec *WorkflowSnapshot) {
			rec.CompletedAt = time.Now()
			if conclusion != "" {
				rec.Result = conclusion
			}
		})
		ctxlog.Info(ctx, "workflow_job completed on github while still running locally", "conclusion", conclusion)
		return
	}

	// The instance is still live and was cancelled on GitHub. Tear the VM down,
	// ensuring diag logs are extracted before the VM is stopped.
	logger := LoggerWithWorkflowInstance(ctxlog.Logger(ctx), inst)
	logger.Info("workflow_job cancelled on github while still running locally", "conclusion", conclusion)
	r.status.upsert(inst.Name, func(rec *WorkflowSnapshot) {
		rec.State = WorkflowCanceled
		rec.Err = "job canceled on github"
		rec.CompletedAt = time.Now()
		if conclusion != "" {
			rec.Result = conclusion
		}
	})
	runErr, stopErr := inst.StopAndReleaseVM(ctx, 30*time.Second)
	if stopErr != nil || runErr != nil {
		logger.Error("failed to stop VM on cancellation", "stop_err", stopErr, "run_err", runErr)
	}
	pushCompletion(ctx, inst, fmt.Errorf("job canceled"))
}

func (r *WorkflowEventHandler) handleQueuedEvent(ctx context.Context, event *gogithub.WorkflowJobEvent, pool *vmsclient.PoolConfig, runner *RunnerConfig) error {

	ctx, cancel := context.WithTimeout(ctx, runner.Timeout)
	defer cancel()

	inst := r.workflowManager.newInstance(ctx, runner, pool, event)
	// Released here rather than after the acquire below: from this point the
	// instance holds a slot in the manager and two open log files, and a
	// failure to acquire a VM or a registration token has to give them back
	// as well. The status record is kept separately and outlives it, so the
	// failure remains visible in the ui.
	defer r.workflowManager.deleteInstance(ctx, inst.Name)

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

	r.status.upsert(inst.Name, func(rec *WorkflowSnapshot) {
		snapshotFromInstance(inst)(rec)
		rec.State = WorkflowRunning
		rec.StartedAt = time.Now()
		if vm := inst.GetVM(); vm != nil {
			rec.VMID = vm.ID()
		}
	})

	runErr := inst.RunJob(ctx, r.clients, r.status)
	// The VM has finished running the job locally at this point; record the
	// vm_completed state. GitHub's "completed" webhook (handleCompleted) will
	// later advance this to the completed state (or if it already arrived, advance now).
	r.status.upsert(inst.Name, func(rec *WorkflowSnapshot) {
		rec.VMCompletedAt = time.Now()
		if inst.JobStarted != nil {
			rec.JobStarted = inst.JobStarted
		}
		if runErr != nil {
			if rec.Result == "" {
				rec.Result = "Failed"
			}
			rec.Err = runErr.Error()
		} else if rec.Result == "" {
			rec.Result = "Succeeded"
		}
		if !rec.CompletedAt.IsZero() {
			rec.State = WorkflowCompleted
		} else {
			rec.State = WorkflowVMCompleted
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
		Action: new("queued"),
		WorkflowJob: new(gogithub.WorkflowJob{
			Name:         new(runner.NamePrefix + "-manual-run"),
			Labels:       labels,
			WorkflowName: new("manual-run"),
		}),
		Repo: new(gogithub.Repository{
			Name:     new(repo),
			Owner:    &gogithub.User{Login: new(owner)},
			FullName: new(wkflowRepoName),
			HTMLURL:  new(fmt.Sprintf("https://github.com/%s", wkflowRepoName)),
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
	if r.completeQueue == nil {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		drained := false
		for _, outcome := range CompletionOutcomes {
			select {
			case e := <-r.completeQueue.Out(outcome):
				r.releaseCompleted(ctx, e, waitForInput)
				drained = true
			case <-ctx.Done():
				return
			default:
			}
		}
		if !drained {
			return
		}
	}
}

// releaseCompleted reports a job taken off the completion queue and deletes
// the VM it ran on. A drained event is no longer the queue's responsibility,
// so the VM is removed from its account before it is deleted.
func (r *WorkflowEventHandler) releaseCompleted(ctx context.Context, e vmspool.CompletionEvent, waitForInput bool) {
	if e.VM == nil {
		return
	}
	logger := e.VM.GetLogger(ctxlog.Logger(ctx))
	if e.Err != nil {
		logger.Error("job failed", "error", e.Err)
	} else {
		logger.Info("job completed successfully")
	}
	vm := e.VM.GetVM()
	if vm == nil {
		return
	}
	r.completeQueue.Remove(vm.PoolName(), vm.ID())
	waitForUserInput(waitForInput, fmt.Sprintf("VM %s kept for debugging; press Enter to release it and continue...", vm.ID()))
	vm.Delete(ctx) //nolint:errcheck
}

func waitForUserInput(keepVM bool, prompt string) {
	if !keepVM {
		return
	}
	fmt.Fprint(os.Stderr, prompt)
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
