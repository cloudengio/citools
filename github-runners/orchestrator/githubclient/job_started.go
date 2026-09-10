// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// JobStartedInfo contains metadata captured by the ACTIONS_RUNNER_HOOK_JOB_STARTED
// hook when GitHub assigns a workflow run to the runner.
type JobStartedInfo struct {
	RunID           int64             `json:"run_id,omitempty"`
	RunNumber       int64             `json:"run_number,omitempty"`
	RunAttempt      int64             `json:"run_attempt,omitempty"`
	Job             string            `json:"job,omitempty"`
	Workflow        string            `json:"workflow,omitempty"`
	Repository      string            `json:"repository,omitempty"`
	RepositoryOwner string            `json:"repository_owner,omitempty"`
	EventName       string            `json:"event_name,omitempty"`
	SHA             string            `json:"sha,omitempty"`
	Ref             string            `json:"ref,omitempty"`
	Actor           string            `json:"actor,omitempty"`
	RunnerName      string            `json:"runner_name,omitempty"`
	Raw             map[string]string `json:"raw,omitempty"`
}

func (j *JobStartedInfo) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	j.Raw = make(map[string]string)
	for k, v := range raw {
		if s, ok := v.(string); ok {
			j.Raw[k] = s
		} else {
			j.Raw[k] = fmt.Sprint(v)
		}
	}
	if v, ok := raw["run_id"]; ok {
		j.RunID = toInt64(v)
	} else if v, ok := raw["GITHUB_RUN_ID"]; ok {
		j.RunID = toInt64(v)
	}
	if v, ok := raw["run_number"]; ok {
		j.RunNumber = toInt64(v)
	} else if v, ok := raw["GITHUB_RUN_NUMBER"]; ok {
		j.RunNumber = toInt64(v)
	}
	if v, ok := raw["run_attempt"]; ok {
		j.RunAttempt = toInt64(v)
	} else if v, ok := raw["GITHUB_RUN_ATTEMPT"]; ok {
		j.RunAttempt = toInt64(v)
	}
	j.Job = getString(raw, "job", "GITHUB_JOB")
	j.Workflow = getString(raw, "workflow", "GITHUB_WORKFLOW")
	j.Repository = getString(raw, "repository", "GITHUB_REPOSITORY")
	j.RepositoryOwner = getString(raw, "repository_owner", "GITHUB_REPOSITORY_OWNER")
	j.EventName = getString(raw, "event_name", "GITHUB_EVENT_NAME")
	j.SHA = getString(raw, "sha", "GITHUB_SHA")
	j.Ref = getString(raw, "ref", "GITHUB_REF")
	j.Actor = getString(raw, "actor", "GITHUB_ACTOR")
	j.RunnerName = getString(raw, "runner_name", "RUNNER_NAME")
	return nil
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}

func getString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
