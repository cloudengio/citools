// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"cloudeng.io/errors"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/vms/vmspool"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
)

type execCommand struct {
	step     string
	cmd      string
	args     []string
	redacted string
}

type selfHostedRunner struct {
	inst            *WorkflowInstance
	runnerDir       string
	repoURL         string
	token           string
	completionQueue *CompletionQueue
}

func newSelfHostedRunner(inst *WorkflowInstance, cq *CompletionQueue, repoURL, token string) *selfHostedRunner {
	return &selfHostedRunner{
		inst:            inst,
		runnerDir:       inst.PoolConfig.RunnerDir(),
		repoURL:         repoURL,
		token:           token,
		completionQueue: cq,
	}
}

func (shr *selfHostedRunner) createConfigCommand() execCommand {
	var args strings.Builder
	fmt.Fprintf(&args, "cd %s && ./config.sh ", shr.runnerDir)
	args.WriteString("--unattended ")
	if shr.inst.RunnerConfig.Ephemeral {
		args.WriteString("--ephemeral ")
	}
	if shr.inst.RunnerConfig.Replace {
		args.WriteString("--replace ")
	}
	fmt.Fprintf(&args, "--url %s --name %s --labels %s",
		shr.repoURL, shr.inst.Name, strings.Join(shr.inst.RunnerConfig.Labels, ","))
	var redacted strings.Builder
	redacted.WriteString(args.String())
	redacted.WriteString(" --token ******")
	fmt.Fprintf(&args, " --token %s", shr.token)
	return execCommand{
		step:     "config",
		cmd:      "bash",
		args:     []string{"-lc", args.String()},
		redacted: fmt.Sprintf("bash -lc %s", redacted.String()),
	}
}

func (shr *selfHostedRunner) createRunCommand() execCommand {
	var args strings.Builder
	fmt.Fprintf(&args, "cd %s && ./run.sh", shr.runnerDir)
	return execCommand{
		step:     "run",
		cmd:      "bash",
		args:     []string{"-lc", args.String()},
		redacted: fmt.Sprintf("bash -lc %s", args.String()),
	}
}

func (shr *selfHostedRunner) createExtractLogsCommand() execCommand {
	var args strings.Builder
	fmt.Fprintf(&args, "cd %s && tar czf - _diag", shr.runnerDir)
	return execCommand{
		step:     "extract-logs",
		cmd:      "bash",
		args:     []string{"-lc", args.String()},
		redacted: fmt.Sprintf("bash -lc %s", args.String()),
	}
}

func (shr *selfHostedRunner) extractLogs(ctx context.Context, vm *vmspool.VM, stdout, stderr io.Writer) error {
	cmd := shr.createExtractLogsCommand()
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := vm.Exec(runCtx, stdout, stderr, cmd.cmd, cmd.args...); err != nil {
		return fmt.Errorf("failed to extract _diag directory: %s: %w", cmd.cmd, err)
	}
	return nil
}

// runQueuedJob configures and runs the job on the VM, extracts diagnostics and
// stops the VM, then pushes the outcome onto the completion queue. It returns
// the local outcome (nil on success) so the caller can record that the VM has
// finished running independently of GitHub's completion webhook.
func (shr *selfHostedRunner) runQueuedJob(ctx context.Context, inst *WorkflowInstance) error {
	vm := inst.GetVM()
	var errs errors.M
	err := shr.runCmds(ctx, vm, inst.RunStdoutStderr, inst.RunStdoutStderr,
		shr.createConfigCommand(),
		shr.createRunCommand())
	errs.Append(err)
	stderr := bytes.NewBuffer(make([]byte, 0, 1024))
	err = shr.extractLogs(ctx, vm, inst.DiagStdout, stderr)
	if err != nil {
		ctxlog.Error(ctx, "failed to extract _diag directory", "vm", vm.ID(), "error", err, "stderr", stderr.String())
		errs.Append(err)
	}

	// Stop the VM
	ctxlog.Info(ctx, "stopping vm", "vm", vm.ID())
	runErr, stopErr := vm.StopAndRelease(ctx, 30*time.Second)
	if stopErr != nil || runErr != nil {
		if stopErr != nil {
			ctxlog.Error(ctx, "failed to stop VM after run error", "vm", vm.ID(), "stop_err", stopErr, "run_err", runErr)
		} else {
			ctxlog.Error(ctx, "successfully stopped VM, but VM has run error", "vm", vm.ID(), "run_err", runErr)
		}
	}
	errs.Append(runErr)
	errs.Append(stopErr)

	ce := vmsclient.CompletionEvent[WorkflowInstance]{Payload: *inst}
	if err := errs.Err(); err != nil {
		shr.completionQueue.PushFailure(ce, err)
		return err
	}
	shr.completionQueue.PushSuccess(ce)
	return nil
}

func (shr *selfHostedRunner) runCmds(ctx context.Context, vm *vmspool.VM, stdout, stderr io.Writer, cmds ...execCommand) error {
	logger := ctxlog.Logger(ctx)
	for _, cmd := range cmds {
		logger.Info("executing command", "step", cmd.step, "command", cmd.redacted)
		if err := vm.Exec(ctx, stdout, stderr, cmd.cmd, cmd.args...); err != nil {
			return fmt.Errorf("failed to execute command %s: %w", cmd.redacted, err)
		}
	}
	return nil
}
