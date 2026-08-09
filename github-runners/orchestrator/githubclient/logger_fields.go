// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"log/slog"

	gogithub "github.com/google/go-github/v89/github"
)

func LogEventGroup(event *gogithub.WorkflowJobEvent) slog.Attr {
	if event == nil {
		return slog.Attr{}
	}
	return slog.Group("event",
		"action", event.GetAction(),
	)
}

func LogJobGroup(job *gogithub.WorkflowJob) slog.Attr {
	if job == nil {
		return slog.Attr{}
	}
	return slog.Group("job",
		"name", job.GetName(),
		"workflow_name", job.GetWorkflowName(),
		"labels", job.GetLabels(),
		"job_id", job.GetID(),
		"run_id", job.GetRunID(),
		"runner_id", job.GetRunnerID(),
		"runner_name", job.GetRunnerName(),
		"runner_group_id", job.GetRunnerGroupID(),
		"runner_group_name", job.GetRunnerGroupName(),
		"run_attempt", job.GetRunAttempt(),
	)
}

func LogRepoGroup(repo *gogithub.Repository) slog.Attr {
	if repo == nil {
		return slog.Attr{}
	}
	return slog.Group("repo",
		"full_name", repo.GetFullName(),
		"owner", repo.GetOwner().GetLogin(),
		"name", repo.GetName(),
	)
}

func LogRunnerConfigGroup(runner *RunnerConfig) slog.Attr {
	if runner == nil {
		return slog.Attr{}
	}
	return slog.Group("runner_config",
		"name_prefix", runner.NamePrefix,
		"ephemeral", runner.Ephemeral,
		"replace", runner.Replace,
		"labels", runner.Labels,
		"vm_pool_name", runner.VMPoolName,
	)
}

func LogWorkflowInstanceGroup(inst *WorkflowInstance) slog.Attr {
	if inst == nil {
		return slog.Attr{}
	}
	vmID := "none"
	vm := inst.GetVM()
	if vm != nil {
		vmID = vm.ID()

	}
	return slog.Group("workflow_instance",
		"name", inst.Name,
		"vm_pool_name", inst.RunnerConfig.VMPoolName,
		"vm_id", vmID,
		"job_log_file", inst.LogName,
		"diag_log_file", inst.DiagName,
	)
}

func LoggerWithEvent(logger *slog.Logger, event *gogithub.WorkflowJobEvent) *slog.Logger {
	if event == nil {
		return logger
	}
	return logger.With(
		LogEventGroup(event),
		LogJobGroup(event.GetWorkflowJob()),
		LogRepoGroup(event.GetRepo()),
	)
}

func LoggerWithWorkflowInstance(logger *slog.Logger, inst *WorkflowInstance) *slog.Logger {
	if inst == nil {
		return logger
	}
	return logger.With(
		LogWorkflowInstanceGroup(inst),
	)
}
