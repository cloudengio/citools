// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"cloudeng.io/errors"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/sync/patterns"
	"cloudeng.io/vms/vmspool"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
	gogithub "github.com/google/go-github/v89/github"
)

type execCommand struct {
	step     string
	cmd      string
	redacted string
}

type selfHostedRunner struct {
	lm              *logFileManager
	runnerCfg       *GitHubRunnerConfig
	poolCfg         *vmsclient.PoolConfig
	repoURL         string
	token           string
	completionQueue *completionEventQueue
}

func newSelfHostedRunner(lm *logFileManager, poolCfg *vmsclient.PoolConfig, runnerCfg *GitHubRunnerConfig, cq *completionEventQueue, repoURL, token string) *selfHostedRunner {
	return &selfHostedRunner{
		lm:              lm,
		runnerCfg:       runnerCfg,
		poolCfg:         poolCfg,
		repoURL:         repoURL,
		token:           token,
		completionQueue: cq,
	}
}

func (shr *selfHostedRunner) createConfigCommand() execCommand {
	var cmd strings.Builder
	fmt.Fprintf(&cmd, `cd %s && ./config.sh `, shr.poolCfg.RunnerDir())
	cmd.WriteString("--unattended ")
	if shr.runnerCfg.Ephemeral {
		cmd.WriteString("--ephemeral ")
	}
	if shr.runnerCfg.Replace {
		cmd.WriteString("--replace ")
	}
	fmt.Fprintf(&cmd, `--url %s --name %s --labels %s`,
		shr.repoURL, shr.runnerCfg.Name, strings.Join(shr.runnerCfg.Labels, ","))
	var redacted strings.Builder
	redacted.WriteString(cmd.String())
	redacted.WriteString(" --token ******")
	fmt.Fprintf(&cmd, ` --token %s`, shr.token)
	return execCommand{
		step:     "config",
		cmd:      fmt.Sprintf("bash -lc '%s'", cmd.String()),
		redacted: fmt.Sprintf("bash -lc '%s'", redacted.String()),
	}
}

func (shr *selfHostedRunner) createRunCommand() execCommand {
	var cmd strings.Builder
	fmt.Fprintf(&cmd, `bash -lc 'cd %s && ./run.sh'`, shr.poolCfg.RunnerDir())
	return execCommand{
		step:     "run",
		cmd:      fmt.Sprintf("bash -lc '%s'", cmd.String()),
		redacted: fmt.Sprintf("bash -lc '%s'", cmd.String()),
	}
}

func (shr *selfHostedRunner) createExtractLogsCommand() execCommand {
	var cmd strings.Builder
	fmt.Fprintf(&cmd, `bash -lc 'cd %s && tar czf - _diag'`, shr.poolCfg.RunnerDir())
	return execCommand{
		step:     "extract-logs",
		cmd:      cmd.String(),
		redacted: cmd.String(),
	}
}

func (shr *selfHostedRunner) extractLogs(ctx context.Context, vm *vmspool.VM, logFile io.Writer) error {
	cmd := shr.createExtractLogsCommand()
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := vm.Exec(runCtx, logFile, logFile, "bash", "-lc", cmd.cmd); err != nil {
		return fmt.Errorf("failed to extract _diag directory: %s: %w", cmd.cmd, err)
	}
	return nil
}

func (shr *selfHostedRunner) runQueuedJob(ctx context.Context, vm *vmspool.VM, event *gogithub.WorkflowJobEvent, timeout time.Duration) {
	var stdout, stderr io.Writer
	logFile, logFileName, err := shr.lm.createJobTemp(shr.runnerCfg.Name, "job", ".log")
	if err != nil {
		ctxlog.Error(ctx, "failed to create temp log file", "err", err)
		stdout = os.Stdout
		stderr = os.Stderr
	} else {
		defer logFile.Close()
		stdout = io.MultiWriter(logFile, os.Stdout)
		stderr = io.MultiWriter(logFile, os.Stderr)
	}
	diagLogFile, diagLogFileName, err := shr.lm.createJobTemp(shr.runnerCfg.Name, "diag", ".tar.gz")
	if err != nil {
		ctxlog.Error(ctx, "failed to create temp diag log file", "err", err)
		diagLogFile = os.Stderr
		diagLogFileName = "stderr"
	} else {
		defer diagLogFile.Close()
	}

	var errs errors.M
	err = shr.runCmds(ctx, vm, stdout, stderr, timeout,
		shr.createConfigCommand(),
		shr.createRunCommand())
	errs.Append(err)
	err = shr.extractLogs(ctx, vm, diagLogFile)
	errs.Append(err)

	ce := CompletionEvent{
		Event:            event,
		RunnerConfig:     shr.runnerCfg,
		VM:               vm,
		JobLogFileName:   logFileName,
		DiagLogsFileName: diagLogFileName,
	}
	if err := errs.Err(); err != nil {
		shr.completionQueue.PushFailure(ce, err)
		return
	}
	shr.completionQueue.PushSuccess(ce)
	return
}

func (shr *selfHostedRunner) execCmd(ctx context.Context, vm *vmspool.VM, cmd execCommand, stdout, stderr io.Writer, timeout time.Duration) error {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return vm.Exec(runCtx, stdout, stderr, cmd.cmd)
}

func (shr *selfHostedRunner) runCmds(ctx context.Context, vm *vmspool.VM, stdout, stderr io.Writer, timeout time.Duration, cmds ...execCommand) error {
	for _, cmd := range cmds {
		if err := shr.execCmd(ctx, vm, cmd, stdout, stderr, timeout); err != nil {
			return fmt.Errorf("failed to execute command %s: %w", cmd.redacted, err)
		}
	}
	return nil
}

type CompletionEvent struct {
	expirationTime   time.Time
	Event            *gogithub.WorkflowJobEvent
	RunnerConfig     *GitHubRunnerConfig
	Err              error
	VM               *vmspool.VM
	JobLogFileName   string
	DiagLogsFileName string
}

func (e *CompletionEvent) Expiration() time.Time {
	return e.expirationTime
}

type completionEventQueue struct {
	successExpiration time.Duration
	failureExpiration time.Duration
	success           patterns.FIFO[CompletionEvent]
	failure           patterns.FIFO[CompletionEvent]
}

func (q *completionEventQueue) PushSuccess(event CompletionEvent) {
	event.expirationTime = time.Now().Add(q.successExpiration)
	q.success.In() <- event
}

func (q *completionEventQueue) PushFailure(event CompletionEvent, err error) {
	event.Err = err
	event.expirationTime = time.Now().Add(q.failureExpiration)
	q.failure.In() <- event
}

func (q *completionEventQueue) expiration(e CompletionEvent) bool {
	return time.Now().After(e.expirationTime)
}

func NewCompletionEventQueue(ctx context.Context, capacity int, successRetention, errorRetention time.Duration) *completionEventQueue {
	q := &completionEventQueue{
		successExpiration: successRetention,
		failureExpiration: errorRetention,
	}
	q.success = *patterns.NewFIFO[CompletionEvent](ctx, capacity,
		patterns.WithPeriodicScan(q.successExpiration, q.expiration))
	q.failure = *patterns.NewFIFO[CompletionEvent](ctx, capacity,
		patterns.WithPeriodicScan(q.failureExpiration, q.expiration))
	return q
}

func (q *completionEventQueue) Success() <-chan CompletionEvent {
	return q.success.Out()
}

func (q *completionEventQueue) Failure() <-chan CompletionEvent {
	return q.failure.Out()
}

func (q *completionEventQueue) Close(ctx context.Context) error {
	var errs errors.M
	close(q.success.In())
	q.success.Stop(ctx)
	q.failure.Stop(ctx)
	for e := range q.success.Out() {
		if e.VM != nil {
			ctxlog.Info(ctx, "releasing vm from success queue", "vm", e.VM.ID())
			errs.Append(e.VM.Release(ctx))
		}
	}
	close(q.failure.In())
	for e := range q.failure.Out() {
		if e.VM != nil {
			ctxlog.Info(ctx, "releasing vm from failure queue", "vm", e.VM.ID())
			errs.Append(e.VM.Release(ctx))
		}
	}

	return errs.Err()
}
