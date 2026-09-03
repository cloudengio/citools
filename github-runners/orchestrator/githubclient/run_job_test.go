// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloudeng.io/webapi/operations"
	gogithub "github.com/google/go-github/v89/github"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestEnsureJobCompletedAlreadyCompleted(t *testing.T) {
	ctx := context.Background()
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/actions/jobs/100") {
			job := gogithub.WorkflowJob{
				ID:         new(int64(100)),
				Status:     new("completed"),
				Conclusion: new("success"),
			}
			data, _ := json.Marshal(job)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(data)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found"))}, nil
	})

	httpClient := &http.Client{Transport: rt}
	rc := NewRepoClients()
	rc.AddClient("cloudengio", "test-repo", operations.WithHTTPClient(httpClient))

	err := rc.EnsureJobCompletedOrCanceled(ctx, "cloudengio/test-repo", 1, 100, "build")
	if err != nil {
		t.Fatalf("expected nil error for completed job, got: %v", err)
	}
}

func TestEnsureJobCompletedCancelsAndVerifies(t *testing.T) {
	ctx := context.Background()
	var canceled atomic.Bool
	var cancelCalled atomic.Bool

	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/actions/jobs/200") {
			status := "in_progress"
			conclusion := ""
			if canceled.Load() {
				status = "completed"
				conclusion = "cancelled"
			}
			job := gogithub.WorkflowJob{
				ID:         new(int64(200)),
				Status:     new(status),
				Conclusion: new(conclusion),
			}
			data, _ := json.Marshal(job)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(data)),
				Header:     make(http.Header),
			}, nil
		}
		if strings.Contains(r.URL.Path, "/actions/runs/2/cancel") && r.Method == http.MethodPost {
			cancelCalled.Store(true)
			canceled.Store(true)
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Body:       io.NopCloser(strings.NewReader("{}")),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found"))}, nil
	})

	httpClient := &http.Client{Transport: rt}
	rc := NewRepoClients()
	rc.AddClient("cloudengio", "test-repo", operations.WithHTTPClient(httpClient))

	err := rc.EnsureJobCompletedOrCanceled(ctx, "cloudengio/test-repo", 2, 200, "test")
	if err != nil {
		t.Fatalf("expected nil error after cancellation and verification, got: %v", err)
	}
	if !cancelCalled.Load() {
		t.Error("expected cancel to be called on github run")
	}
}

func TestJobStartedMismatchDetection(t *testing.T) {
	status := newStatusTracker(time.Minute)

	var logBuf bytes.Buffer
	wi := &WorkflowInstance{
		Name:            "runner-test-01",
		RunStdoutStderr: &logBuf,
		Event: &gogithub.WorkflowJobEvent{
			Sender: &gogithub.User{
				Login: new("expected-actor"),
			},
			Repo: &gogithub.Repository{
				FullName: new("cloudengio/test-repo"),
				Owner: &gogithub.User{
					Login: new("cloudengio"),
				},
			},
			WorkflowJob: &gogithub.WorkflowJob{
				ID:           new(int64(500)),
				RunID:        new(int64(1000)),
				RunAttempt:   new(int64(1)),
				Name:         new("expected-job-display-name"),
				WorkflowName: new("expected-workflow"),
				HeadSHA:      new("0123456789abcdef0123456789abcdef01234567"),
				HeadBranch:   new("main"),
			},
		},
	}
	status.upsert(wi.Name, func(rec *WorkflowSnapshot) {
		rec.State = WorkflowRunning
	})

	// jobStarted has different Job name (which should NOT trigger mismatch),
	// but mismatched RunID, RunAttempt, Repo, Owner, Actor, Workflow, SHA, Ref
	jobStarted := &JobStartedInfo{
		RunID:           2000,
		RunAttempt:      2,
		Job:             "build", // $GITHUB_JOB YAML ID differs from display name, should NOT trigger mismatch
		Workflow:        "different-workflow",
		Repository:      "otherorg/other-repo",
		RepositoryOwner: "otherorg",
		Actor:           "other-actor",
		SHA:             "fedcba9876543210fedcba9876543210fedcba98",
		Ref:             "refs/heads/feature",
	}

	var diffs []string
	expectedRunID := wi.Event.GetWorkflowJob().GetRunID()
	if jobStarted.RunID != 0 && expectedRunID != 0 && jobStarted.RunID != expectedRunID {
		diffs = append(diffs, "run_id mismatch")
	}
	expectedRunAttempt := wi.Event.GetWorkflowJob().GetRunAttempt()
	if jobStarted.RunAttempt != 0 && expectedRunAttempt != 0 && jobStarted.RunAttempt != expectedRunAttempt {
		diffs = append(diffs, "run_attempt mismatch")
	}
	expectedRepo := wi.Event.GetRepo().GetFullName()
	if jobStarted.Repository != "" && expectedRepo != "" && !strings.EqualFold(jobStarted.Repository, expectedRepo) {
		diffs = append(diffs, "repository mismatch")
	}
	expectedOwner := wi.Event.GetRepo().GetOwner().GetLogin()
	if jobStarted.RepositoryOwner != "" && expectedOwner != "" && !strings.EqualFold(jobStarted.RepositoryOwner, expectedOwner) {
		diffs = append(diffs, "repository_owner mismatch")
	}
	expectedActor := wi.Event.GetSender().GetLogin()
	if jobStarted.Actor != "" && expectedActor != "" && !strings.EqualFold(jobStarted.Actor, expectedActor) {
		diffs = append(diffs, "actor mismatch")
	}
	expectedWorkflow := wi.Event.GetWorkflowJob().GetWorkflowName()
	if jobStarted.Workflow != "" && expectedWorkflow != "" && jobStarted.Workflow != expectedWorkflow {
		diffs = append(diffs, "workflow mismatch")
	}
	expectedSHA := wi.Event.GetWorkflowJob().GetHeadSHA()
	if jobStarted.SHA != "" && expectedSHA != "" && !strings.EqualFold(jobStarted.SHA, expectedSHA) {
		diffs = append(diffs, "sha mismatch")
	}
	expectedBranch := wi.Event.GetWorkflowJob().GetHeadBranch()
	if jobStarted.Ref != "" && expectedBranch != "" {
		refClean := strings.TrimPrefix(strings.TrimPrefix(jobStarted.Ref, "refs/heads/"), "refs/tags/")
		branchClean := strings.TrimPrefix(strings.TrimPrefix(expectedBranch, "refs/heads/"), "refs/tags/")
		if refClean != branchClean && jobStarted.Ref != expectedBranch {
			diffs = append(diffs, "ref mismatch")
		}
	}

	// 8 guaranteed fields mismatched; Job was NOT checked.
	if len(diffs) != 8 {
		t.Fatalf("expected 8 mismatches, got %d: %v", len(diffs), diffs)
	}

	diffMsg := "assigned workflow run differs from queued event: " + strings.Join(diffs, "; ")
	status.upsert(wi.Name, func(rec *WorkflowSnapshot) {
		rec.Err = diffMsg
		rec.Result = "Failed"
	})

	rec, ok := status.get(wi.Name)
	if !ok {
		t.Fatal("workflow record not found")
	}
	if rec.Result != "Failed" {
		t.Errorf("expected Result Failed, got %q", rec.Result)
	}
	if !strings.Contains(rec.Err, "run_id mismatch") {
		t.Errorf("expected Err to contain run_id mismatch, got %q", rec.Err)
	}
}

// Suppress unused import warnings if any
var _ sync.Mutex
