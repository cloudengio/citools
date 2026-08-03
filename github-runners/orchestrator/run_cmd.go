// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"cloudeng.io/cmdutil/flags"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/vms/vmspool"
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

func (l *logFileManager) createJobTemp(runnerName, step, ext string) (*os.File, string, error) {
	logFile, err := os.CreateTemp(l.dir, runnerName+"-"+step+"-*"+ext)
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

	summary, err := r.runJob(ctx, logger, runner.Name, wkflowRepoName, runner, false)
	logger = logger.With(
		"job_logfile", summary.jobLogFileName,
		"diag_logfile", summary.diagLogFileName,
	)
	if err != nil {
		logger.Error("job summary: failed", "err", err)
	} else {
		logger.Info("job summary: completed successfully")
	}
	return err
}

type execCommand struct {
	step     string
	cmd      string
	redacted string
}

func (r *workflowEventHandler) createConfigCommand(runnerDir string, runnerName string, runnerCfg GitHubRunnerConfig, fullRepoName, token string) execCommand {
	var out strings.Builder
	fmt.Fprintf(&out, `cd %s && ./config.sh `, runnerDir)
	out.WriteString("--unattended ")
	if runnerCfg.Ephemeral {
		out.WriteString("--ephemeral ")
	}
	if runnerCfg.Replace {
	}
	out.WriteString("--replace ")
	url := fmt.Sprintf("https://github.com/%s", fullRepoName)
	fmt.Fprintf(&out, `--url %s --name %s --labels %s`,
		url, runnerName, strings.Join(runnerCfg.Labels, ","))
	var rout strings.Builder
	rout.WriteString(out.String())
	rout.WriteString(" --token ******")
	fmt.Fprintf(&out, ` --token %s`, token)
	return execCommand{
		step:     "config",
		cmd:      out.String(),
		redacted: rout.String(),
	}
}

func (r *workflowEventHandler) createRunCommand(runnerDir string) execCommand {
	var out strings.Builder
	fmt.Fprintf(&out, `cd %s && ./run.sh`, runnerDir)
	return execCommand{
		step:     "run",
		cmd:      out.String(),
		redacted: out.String(),
	}
}

func (r *workflowEventHandler) createVMCommands(poolCfg vmsclient.PoolConfig, runnerName string, runnerCfg GitHubRunnerConfig, fullRepoName, token string) []execCommand {
	runnerDir := poolCfg.RunnerDir()
	cmds := []execCommand{
		r.createConfigCommand(runnerDir, runnerName, runnerCfg, fullRepoName, token),
		r.createRunCommand(runnerDir),
	}
	return cmds
}

func (r *workflowEventHandler) extractLogs(ctx context.Context, vm *vmspool.VM, logger *slog.Logger, runnerDir, runnerName string) (string, error) {
	var extractDiagsCmd strings.Builder
	fmt.Fprintf(&extractDiagsCmd, `tar czf - -C %s _diag`, runnerDir)

	diagLogFile, diagLogFileName, err := r.lm.createJobTemp(runnerName, "gh-diags", ".tgz")
	if err != nil {
		logger.Error("failed to create diag log file", "err", err)
		return "", err
	}
	defer diagLogFile.Close()

	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := vm.Exec(runCtx, diagLogFile, diagLogFile, "bash", "-lc", extractDiagsCmd.String()); err != nil {
		logger.Error("failed to extract _diag directory", "err", err, "cmd", extractDiagsCmd.String(), "logfile", diagLogFileName)
		return "", err
	}
	logger.Info("extracted _diag directory", "logfile", diagLogFileName)
	return diagLogFileName, nil
}

func (r *workflowEventHandler) execCmd(ctx context.Context, vm *vmspool.VM, logger *slog.Logger, cmd execCommand, stdout, stderr io.Writer, timeout time.Duration, keepVM bool) error {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	logger.Info("executing command", "cmd", cmd.redacted)
	if err := vm.Exec(runCtx, stdout, stderr, "bash", "-lc", cmd.cmd); err != nil {
		if keepVM {
			logger.Info("keeping VM for debugging", "step", cmd.step, "vm_id", vm.ID())
			fmt.Fprintf(os.Stderr, "VM %s step %s kept for debugging; press Enter to release it and continue...", vm.ID(), cmd.step)
			if _, rerr := bufio.NewReader(os.Stdin).ReadString('\n'); rerr != nil {
				logger.Warn("failed to read input while waiting to release debug VM", "err", rerr)
			}
		}
		return err
	}
	return nil
}

type jobRunSummary struct {
	jobLogFileName  string
	diagLogFileName string
}

func (r *workflowEventHandler) runJob(ctx context.Context, logger *slog.Logger, runnerName, fullName string, runner GitHubRunnerConfig, keepVM bool) (*jobRunSummary, error) {

	gc, ok := repoClients.GetClientFullName(fullName)
	if !ok {
		return nil, fmt.Errorf("no GitHub client found for %s", fullName)
	}

	tok, err := gc.GetRegistrationToken(ctx)
	if err != nil {
		return nil, err
	}
	logger.Info("obtained github registration token", "expires_at", tok.GetExpiresAt())

	vm, err := r.vmPools.GetVM(ctx, runner.VMPoolName)
	if err != nil {
		logger.Error("failed to acquire VM from pool", "err", err)
		return nil, err
	}
	defer vm.Release(ctx)

	poolConfig, ok := r.poolConfigs[runner.VMPoolName]
	if !ok {
		return nil, fmt.Errorf("no pool configuration found for VM pool %s", runner.VMPoolName)
	}

	cmds := r.createVMCommands(poolConfig, runnerName, runner, fullName, tok.GetToken())

	var stdout, stderr io.Writer

	logFile, logFileName, err := r.lm.createJobTemp(runnerName, "job", ".log")
	if err != nil {
		logger.Error("failed to create temp log file", "err", err)
		stdout = os.Stdout
		stderr = os.Stderr
	} else {
		defer logFile.Close()
		stdout = io.MultiWriter(logFile, os.Stdout)
		stderr = io.MultiWriter(logFile, os.Stderr)
	}

	summary := &jobRunSummary{
		jobLogFileName: logFileName,
	}

	logger = logger.With("logfile", logFileName)
	for _, cmd := range cmds {
		if err := r.execCmd(ctx, vm, logger, cmd, stdout, stderr, runner.Timeout, keepVM); err != nil {
			logger.Error("failed to execute command", "err", err, "cmd", cmd.redacted, "logfile", logFileName)
			return summary, err
		}
	}
	diagFileName, err := r.extractLogs(ctx, vm, logger, poolConfig.RunnerDir(), runnerName)
	summary.diagLogFileName = diagFileName
	return summary, err
}

func (r *workflowEventHandler) RunJob(ctx context.Context, onwer, repo string, labels []string, keepVM bool) error {
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

	summary, err := r.runJob(ctx, logger, runner.Name, wkflowRepoName, runner, keepVM)
	logger = logger.With(
		"job_logfile", summary.jobLogFileName,
		"diag_logfile", summary.diagLogFileName,
	)
	if err != nil {
		logger.Error("job summary: failed", "err", err)
	} else {
		logger.Info("job summary: completed successfully")
	}
	return err
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

	return wh.RunJob(ctx, rc.Service.Owner, rc.Service.Repo, fv.Labels.Values, fv.KeepVM)
}
