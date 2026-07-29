// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"cloudeng.io/cmdutil/flags"
	"cloudeng.io/logging/ctxlog"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubwebhook"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
	gogithub "github.com/google/go-github/v89/github"
)

type logFileManager struct {
	dir     string
	logFile *os.File
}

func newLogFileManager(dir string) (*logFileManager, error) {
	dir, err := os.MkdirTemp("", dir)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	logFile, err := os.CreateTemp(dir, "vmspool-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	fmt.Printf("created temp log file: %s\n", logFile.Name())
	return &logFileManager{
		dir:     dir,
		logFile: logFile,
	}, nil
}

func (l *logFileManager) Close() {
	if l.logFile != nil {
		_ = l.logFile.Close()
	}
}

func (l *logFileManager) CreateTemp(id string) io.Writer {
	if l.logFile == nil {
		return io.Discard
	}
	return l.logFile
}

func (l *logFileManager) createJobTemp(runnerName string) (*os.File, string, error) {
	logFile, err := os.CreateTemp(l.dir, runnerName+"-job-*")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp file: %w", err)
	}
	return logFile, logFile.Name(), nil
}

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

	lm, err := newLogFileManager(cfg.TmpDir)
	if err != nil {
		return err
	}
	defer lm.Close()

	vmPools, err := vmsclient.NewPools(ctx, cfg.VMPools, lm.CreateTemp)
	if err != nil {
		return err
	}
	defer vmPools.Close(ctx)

	wh := newWorkflowEventHandler(ctx, lm, cfg.VMPools, cfg.Repositories, vmPools)

	h := githubwebhook.New(cfg.Webhook.RelayURL, wh.handle)
	return h.Listen(ctx, opts)
}

type workflowEventHandler struct {
	runnerConfig map[string]GitHubRunnerConfig
	vmPools      *vmsclient.Pools
	poolConfigs  map[string]vmsclient.PoolConfig
	lm           *logFileManager
}

func newWorkflowEventHandler(ctx context.Context, lm *logFileManager, poolConfigs map[string]vmsclient.PoolConfig, repoConfigs []RepositoryConfig, vmPools *vmsclient.Pools) *workflowEventHandler {
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
		runnerConfig: runnerMap,
		vmPools:      vmPools,
		poolConfigs:  poolConfigs,
		lm:           lm,
	}
}

func (r *workflowEventHandler) handle(ctx context.Context, event *gogithub.WorkflowJobEvent) error {
	action := event.GetAction()
	job := event.GetWorkflowJob()
	if job == nil {
		ctxlog.Error(ctx, "workflow_job event has no workflow_job", "action", action)
	}

	logger := ctxlog.Logger(ctx).With(
		"workflow_name", job.GetWorkflowName(),
		"id", job.GetID(),
		"run_id", job.GetRunID(),
		"action", action)

	if action != "queued" {
		logger.Info("workflow_job event ignored: action is not 'queued'")
		return nil
	}

	wkflowRepo := event.GetRepo()
	if wkflowRepo == nil {
		ctxlog.Error(ctx, "workflow_job event has no repository property: ignored")
		return nil
	}

	wkflowRepoName := wkflowRepo.GetFullName()
	logger = logger.With("repo_full_name", wkflowRepoName)

	labels := job.GetLabels()
	if len(labels) == 0 {
		ctxlog.Error(ctx, "workflow_job event has no labels: ignored")
		return nil
	}

	labelKey := wkflowRepoName + ":" + canonicalLabelSet(labels)
	runner, ok := r.runnerConfig[labelKey]
	if !ok {
		ctxlog.Error(ctx, "workflow_job event has no corresponding local runner configuration: ignored", "label_key", labelKey)
		return nil
	}
	logger = logger.With(
		"label_key", labelKey,
		"runner_name", runner.Name,
		"vm_pool_name", runner.VMPoolName)

	return r.runJob(ctx, logger, job.GetName(), wkflowRepoName, runner)
}

func (r *workflowEventHandler) vmCommand(poolCfg vmsclient.PoolConfig, runnerName string, runnerCfg GitHubRunnerConfig, fullRepoName, token string) (redacted, fullCmd string) {
	var out strings.Builder
	fmt.Fprintf(&out, `cd %s && ./config.sh `, poolCfg.RunnerDir())
	out.WriteString("--unattended ")
	if runnerCfg.Ephemeral {
		out.WriteString("--ephemeral ")
	}
	if runnerCfg.Replace {
		out.WriteString("--replace ")
	}
	url := fmt.Sprintf("https://github.com/%s", fullRepoName)
	fmt.Fprintf(&out, `--url %s --name %s --labels %s`,
		url, runnerName, strings.Join(runnerCfg.Labels, ","))
	var rout strings.Builder
	rout.WriteString(out.String())
	rout.WriteString(" --token ******")
	fmt.Fprintf(&out, ` --token %s`, token)
	fmt.Fprintf(&out, ` && ./run.sh`)
	fmt.Fprintf(&rout, ` && ./run.sh`)
	grepCmd := ` && cat /Users/admin/actions-runner/_diag/Worker_*.log`
	out.WriteString(grepCmd)
	rout.WriteString(grepCmd)
	redacted = rout.String()
	fullCmd = out.String()
	return redacted, fullCmd
}

func (r *workflowEventHandler) runJob(ctx context.Context, logger *slog.Logger, runnerName, fullName string, runner GitHubRunnerConfig) error {

	gc, ok := repoClients.GetClientFullName(fullName)
	if !ok {
		return fmt.Errorf("no GitHub client found for %s", fullName)
	}

	tok, err := gc.GetRegistrationToken(ctx)
	if err != nil {
		return err
	}
	logger.Info("obtained github registration token", "expires_at", tok.GetExpiresAt())

	vm, err := r.vmPools.GetVM(ctx, runner.VMPoolName)
	if err != nil {
		logger.Error("failed to acquire VM from pool", "err", err)
		return err
	}
	defer vm.Release(ctx)

	poolConfig, ok := r.poolConfigs[runner.VMPoolName]
	if !ok {
		return fmt.Errorf("no pool configuration found for VM pool %s", runner.VMPoolName)
	}

	redacted, configCmd := r.vmCommand(poolConfig, runnerName, runner, fullName, tok.GetToken())

	var stdout, stderr io.Writer

	logFile, logFileName, err := r.lm.createJobTemp(runnerName)
	if err != nil {
		logger.Error("failed to create temp log file", "err", err)
		stdout = os.Stdout
		stderr = os.Stderr
	} else {
		defer logFile.Close()
		stdout = io.MultiWriter(logFile, os.Stdout)
		stderr = io.MultiWriter(logFile, os.Stderr)
	}

	logger.Info("configuring runner", "cmd", redacted, "logfile", logFileName)

	runCtx, cancel := context.WithTimeout(ctx, runner.Timeout)
	defer cancel()

	if err := vm.Exec(runCtx, stdout, stderr, "bash", "-lc", configCmd); err != nil {
		logger.Error("failed to execute config command", "err", err, "cmd", redacted, "logfile", logFileName)
		return err
	}

	logger.Info("runner configured", "cmd", redacted, "logfile", logFileName)

	return nil

}

func (r *workflowEventHandler) RunJob(ctx context.Context, onwer, repo string, labels []string) error {
	wkflowRepoName := onwer + "/" + repo
	labelKey := wkflowRepoName + ":" + canonicalLabelSet(labels)
	runner, ok := r.runnerConfig[labelKey]
	if !ok {
		return fmt.Errorf("no runner configuration found for labels %v, key %v", labels, labelKey)
	}

	logger := ctxlog.Logger(ctx).With(
		"runner_name", runner.Name,
		"repo_full_name", wkflowRepoName,
		"labels", labels,
		"vm_pool_name", runner.VMPoolName)

	return r.runJob(ctx, logger, runner.Name, wkflowRepoName, runner)
}

type RunJobFlags struct {
	GitHubFlags
	Labels flags.Commas `subcmd:"labels,,'labels to select the runner to use, may be repeated and may be comma separated'"`
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

	lm, err := newLogFileManager(cfg.TmpDir)
	if err != nil {
		return err
	}
	defer lm.Close()

	vmPools, err := vmsclient.NewPools(ctx, cfg.VMPools, lm.CreateTemp)
	if err != nil {
		return err
	}
	defer vmPools.Close(ctx)

	wh := newWorkflowEventHandler(ctx, lm, cfg.VMPools, []RepositoryConfig{rc}, vmPools)

	return wh.RunJob(ctx, rc.Service.Owner, rc.Service.Repo, fv.Labels.Values)
}
