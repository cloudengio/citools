// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"encoding/json"
	"testing"
)

func TestJobStartedInfoUnmarshal(t *testing.T) {
	input := `{
  "GITHUB_RUN_ID": "12345678",
  "GITHUB_RUN_NUMBER": "42",
  "GITHUB_RUN_ATTEMPT": "1",
  "GITHUB_JOB": "test-job",
  "GITHUB_WORKFLOW": "CI",
  "GITHUB_REPOSITORY": "cloudengio/citools",
  "GITHUB_REPOSITORY_OWNER": "cloudengio",
  "GITHUB_EVENT_NAME": "workflow_dispatch",
  "GITHUB_SHA": "0123456789abcdef",
  "GITHUB_REF": "refs/heads/main",
  "GITHUB_ACTOR": "octocat",
  "RUNNER_NAME": "macos-runner-01",
  "run_id": 12345678,
  "job": "test-job",
  "workflow": "CI",
  "repository": "cloudengio/citools"
}`

	var info JobStartedInfo
	if err := json.Unmarshal([]byte(input), &info); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if info.RunID != 12345678 {
		t.Errorf("expected RunID 12345678, got %d", info.RunID)
	}
	if info.RunNumber != 42 {
		t.Errorf("expected RunNumber 42, got %d", info.RunNumber)
	}
	if info.RunAttempt != 1 {
		t.Errorf("expected RunAttempt 1, got %d", info.RunAttempt)
	}
	if info.Job != "test-job" {
		t.Errorf("expected Job 'test-job', got %q", info.Job)
	}
	if info.Workflow != "CI" {
		t.Errorf("expected Workflow 'CI', got %q", info.Workflow)
	}
	if info.Repository != "cloudengio/citools" {
		t.Errorf("expected Repository 'cloudengio/citools', got %q", info.Repository)
	}
	if info.RepositoryOwner != "cloudengio" {
		t.Errorf("expected RepositoryOwner 'cloudengio', got %q", info.RepositoryOwner)
	}
	if info.EventName != "workflow_dispatch" {
		t.Errorf("expected EventName 'workflow_dispatch', got %q", info.EventName)
	}
	if info.SHA != "0123456789abcdef" {
		t.Errorf("expected SHA '0123456789abcdef', got %q", info.SHA)
	}
	if info.Ref != "refs/heads/main" {
		t.Errorf("expected Ref 'refs/heads/main', got %q", info.Ref)
	}
	if info.Actor != "octocat" {
		t.Errorf("expected Actor 'octocat', got %q", info.Actor)
	}
	if info.RunnerName != "macos-runner-01" {
		t.Errorf("expected RunnerName 'macos-runner-01', got %q", info.RunnerName)
	}
	if info.Raw["GITHUB_RUN_ID"] != "12345678" {
		t.Errorf("expected Raw[GITHUB_RUN_ID] '12345678', got %q", info.Raw["GITHUB_RUN_ID"])
	}
}

func TestJobStartedInfoUnmarshalFallback(t *testing.T) {
	// Test only uppercase GITHUB_ prefixed strings
	input := `{
  "GITHUB_RUN_ID": "98765432",
  "GITHUB_JOB": "build",
  "GITHUB_WORKFLOW": "Release"
}`

	var info JobStartedInfo
	if err := json.Unmarshal([]byte(input), &info); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if info.RunID != 98765432 {
		t.Errorf("expected RunID 98765432, got %d", info.RunID)
	}
	if info.Job != "build" {
		t.Errorf("expected Job 'build', got %q", info.Job)
	}
	if info.Workflow != "Release" {
		t.Errorf("expected Workflow 'Release', got %q", info.Workflow)
	}
}
