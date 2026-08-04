// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"context"

	"fmt"
	"log/slog"
	"os"

	"cloudeng.io/cmdutil/flags"
	"cloudeng.io/errors"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/sync/errgroup"
	"cloudeng.io/vms/vmspool"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubwebhook"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
	gogithub "github.com/google/go-github/v89/github"
)

type RunCommand struct{}

func (r RunCommand) Run(ctx context.Context, _ any, _ []string) error {
	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return fmt.Errorf("no config in context")
	}

	if cfg.Webhook.RelayURL == "" {
		return fmt.Errorf("no relay URL configured")
	}

	opts, err := cfg.Webhook.Options()
	if err != nil {
		return err
	}

	wh, err := newWorkflowEventHandler(ctx, cfg.Global, cfg.VMPools, cfg.Repositories)
	if err != nil {
		return err
	}

	defer wh.Close(context.Background())

	h := githubwebhook.New(cfg.Webhook.RelayURL, wh.handle)
	return h.Listen(ctx, opts)
}

type workflowEventHandler struct {
	runnerConfig  map[string]GitHubRunnerConfig
	vmPools       *vmsclient.Pools
	poolConfigs   map[string]vmsclient.PoolConfig
	completeQueue *completionEventQueue
	lm            *logFileManager
}

func newWorkflowEventHandler(ctx context.Context, cfg GlobalConfig, poolConfigs map[string]vmsclient.PoolConfig, repoConfigs []RepositoryConfig) (*workflowEventHandler, error) {
	lm, err := newLogFileManager(cfg.TmpDir)
	if err != nil {
		return nil, err
	}

	vmPools, err := vmsclient.NewPools(ctx, poolConfigs, lm.CreateTemp)
	if err != nil {
		return nil, err
	}
	// defer vmPools.Close(ctx) // Do not close here, let the caller manage the lifecycle

	runnerMap := make(map[string]GitHubRunnerConfig, len(repoConfigs)*2)
	for _, rc := range repoConfigs {
		fullname := rc.Service.Owner + "/" + rc.Service.Repo
		for _, runner := range rc.Runners {
			labelKey := fullname + ":" + canonicalLabelSet(runner.Labels)
			runnerMap[labelKey] = runner
			ctxlog.Info(ctx, "handling workflow events", "repo", fullname, "runner", runner.Name, "label_key", labelKey)
		}
	}
	return &workflowEventHandler{
		runnerConfig:  runnerMap,
		vmPools:       vmPools,
		poolConfigs:   poolConfigs,
		completeQueue: NewCompletionEventQueue(ctx, cfg.CompletionQueueSize, cfg.SuccessfulVMRetentionPeriod, cfg.FailedVMRetentionPeriod),
		lm:            lm,
	}, nil
}

func (r *workflowEventHandler) Close(ctx context.Context) error {
	r.lm.Close()
	var errs errors.M
	errs.Append(r.vmPools.Close(ctx))
	errs.Append(r.completeQueue.Close(ctx))
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
// labels and action. Currently only queued and cancelled actions are supported, if an
// action is not supported, the event is ignored and nil is returned along with a nil error.
func (r *workflowEventHandler) matchOnLabelsAndAction(ctx context.Context, event *gogithub.WorkflowJobEvent) (*slog.Logger, *gogithub.WorkflowJobEvent, *GitHubRunnerConfig, *vmsclient.PoolConfig, error) {
	logger := ctxlog.Logger(ctx)
	action := event.GetAction()
	if action != "queued" && action != "cancelled" {
		return logger, nil, nil, nil, errUnsupportedAction
	}

	logger = logger.With("event_action", action)
	job := event.GetWorkflowJob()
	if job == nil {
		return logger, nil, nil, nil, errNoJob
	}

	labels := job.GetLabels()
	logger = logger.WithGroup("job").With(
		"name", job.GetName(),
		"workflow_name", job.GetWorkflowName(),
		"labels", labels,
		"id", job.GetID(),
		"run_id", job.GetRunID(),
		"runner_id", job.GetRunnerID(),
		"runner_name", job.GetRunnerName(),
		"runner_group_id", job.GetRunnerGroupID(),
		"runner_group_name", job.GetRunnerGroupName(),
		"run_attempt", job.GetRunAttempt(),
	)
	if len(labels) == 0 {
		return logger, nil, nil, nil, errNoLabels
	}

	wkflowRepo := event.GetRepo()
	if wkflowRepo == nil {
		return logger, nil, nil, nil, errNoRepo
	}

	wkflowRepoName := wkflowRepo.GetFullName()
	labelKey := wkflowRepoName + ":" + canonicalLabelSet(labels)
	logger = logger.With("repo_full_name", wkflowRepoName, "label_key", labelKey)
	runner, ok := r.runnerConfig[labelKey]
	if !ok {
		return logger, nil, nil, nil, errNoRunnerConfig
	}
	pool, ok := r.poolConfigs[runner.VMPoolName]
	if !ok {
		return logger, nil, nil, nil, errNoVMPoolConfig
	}
	logger = logger.With("runner_name", runner.Name, "vm_pool_name", runner.VMPoolName)
	return logger, event, &runner, &pool, nil
}

func (r *workflowEventHandler) handle(ctx context.Context, event *gogithub.WorkflowJobEvent) error {
	logger, event, runner, pool, err := r.matchOnLabelsAndAction(ctx, event)
	if err != nil {
		if errors.Is(err, errUnsupportedAction) {
			logger.Info("workflow_job event ignored: action is not 'queued' or 'cancelled'", "action", event.GetAction())
			return nil
		}
		logger.Error("workflow_job event ignored due to error", "err", err)
		return nil
	}
	ctx = ctxlog.WithLogger(ctx, logger)
	switch event.GetAction() {
	case "queued":
		return r.handleQueuedEvent(ctx, event, pool, runner)
	default:
		logger.Info("workflow_job event ignored: unsupported action", "action", event.GetAction())
		return nil
	}
}

func (r *workflowEventHandler) getTokenAndVM(ctx context.Context, event *gogithub.WorkflowJobEvent, runner *GitHubRunnerConfig, pool *vmsclient.PoolConfig) (*gogithub.RegistrationToken, *vmspool.VM, error) {
	var g errgroup.T

	var token *gogithub.RegistrationToken
	var vm *vmspool.VM
	g.Go(func() error {
		var err error
		repoFullName := event.GetRepo().GetFullName()
		token, err = repoClients.GetTokenFullName(ctx, repoFullName)
		if err != nil {
			return fmt.Errorf("failed to get registration token for %s: %w", repoFullName, err)
		}
		return nil
	})

	g.Go(func() error {
		var err error
		vm, err = r.vmPools.GetVM(ctx, runner.VMPoolName)
		if err != nil {
			return fmt.Errorf("failed to acquire VM from pool %s: %w", runner.VMPoolName, err)
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		if vm != nil {
			vm.Release(ctx)
		}
		return nil, nil, err
	}
	return token, vm, nil
}

func (r *workflowEventHandler) handleQueuedEvent(ctx context.Context, event *gogithub.WorkflowJobEvent, pool *vmsclient.PoolConfig, runner *GitHubRunnerConfig) error {
	token, vm, err := r.getTokenAndVM(ctx, event, runner, pool)
	if err != nil {
		return err
	}
	shr := newSelfHostedRunner(r.lm, pool, runner, r.completeQueue, event.GetRepo().GetURL(), token.GetToken())
	shr.runQueuedJob(ctx, vm, event, runner.Timeout)
	return nil
}

func (r *workflowEventHandler) RunJob(ctx context.Context, owner, repo string, labels []string, keepVM bool) error {
	wkflowRepoName := owner + "/" + repo
	labelKey := wkflowRepoName + ":" + canonicalLabelSet(labels)
	runner, ok := r.runnerConfig[labelKey]
	if !ok {
		return fmt.Errorf("no runner configuration found for labels %v, key %v", labels, labelKey)
	}

	ctx = ctxlog.WithLogger(ctx, ctxlog.Logger(ctx).With(
		"runner_name", runner.Name,
		"repo_full_name", wkflowRepoName,
		"labels", labels,
		"vm_pool_name", runner.VMPoolName))

	pool, ok := r.poolConfigs[runner.VMPoolName]
	if !ok {
		return fmt.Errorf("no pool configuration found for VM pool %s", runner.VMPoolName)
	}

	event := &gogithub.WorkflowJobEvent{
		Action: gogithub.Ptr("queued"),
		WorkflowJob: &gogithub.WorkflowJob{
			Name:         &runner.Name,
			Labels:       labels,
			WorkflowName: gogithub.Ptr("manual-run"),
		},
		Repo: &gogithub.Repository{
			Name:  &repo,
			Owner: &gogithub.User{Login: &owner},
		},
	}
	if err := r.handleQueuedEvent(ctx, event, &pool, &runner); err != nil {
		return fmt.Errorf("failed to handle queued event: %w", err)
	}

	for success := range r.completeQueue.Success() {
		ctxlog.Info(ctx, "job completed successfully", "runner_name", success.RunnerConfig.Name, "vm_pool_name", success.RunnerConfig.VMPoolName, "job_id", success.Event.GetWorkflowJob().GetID())
		waitForInput(keepVM, fmt.Sprintf("VM %s kept for debugging; press Enter to release it and continue...", success.VM.ID()))
	}

	for failure := range r.completeQueue.Failure() {
		ctxlog.Error(ctx, "job failed", "runner_name", failure.RunnerConfig.Name, "vm_pool_name", failure.RunnerConfig.VMPoolName, "job_id", failure.Event.GetWorkflowJob().GetID(), "error", failure.Err)
		waitForInput(keepVM, fmt.Sprintf("VM %s kept for debugging; press Enter to release it and continue...", failure.VM.ID()))
	}
	return nil
}

func waitForInput(keepVM bool, prompt string) {
	if !keepVM {
		return
	}
	fmt.Fprint(os.Stderr, prompt)
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

type RunJobFlags struct {
	GitHubFlags
	Labels flags.Commas `subcmd:"labels,,'labels to select the runner to use, may be repeated and may be comma separated'"`
	KeepVM bool         `subcmd:"keep-vm,,'if set the VM will not be released back to the pool after the job has completed to allow for debugging'"`
}

func (r RunCommand) RunJob(ctx context.Context, flags any, _ []string) error {
	fv := flags.(*RunJobFlags)
	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return fmt.Errorf("no config in context")
	}

	rc, err := LookupRepositoryConfig(cfg, fv.GitHubFlags)
	if err != nil {
		return err
	}

	wh, err := newWorkflowEventHandler(ctx, cfg.Global, cfg.VMPools, []RepositoryConfig{rc})
	if err != nil {
		return err
	}
	defer wh.Close(ctx)

	return wh.RunJob(ctx, rc.Service.Owner, rc.Service.Repo, fv.Labels.Values, fv.KeepVM)
}
