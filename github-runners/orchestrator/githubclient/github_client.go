// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"context"
	"fmt"
	"sync"
	"time"

	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/webapi/clients/github"
	"cloudeng.io/webapi/operations"
	gogithub "github.com/google/go-github/v89/github"
)

// Repo provides a client for GitHub repository operations. There will be a
// separate client for each repository that is being managed.
type Repo struct {
	owner, repo string
	opts        []operations.Option
	mu          sync.Mutex
	token       *gogithub.RegistrationToken
}

func New(owner, repo string, opts ...operations.Option) *Repo {
	return &Repo{
		owner: owner,
		repo:  repo,
		opts:  opts,
	}
}

func (c *Repo) existingToken() *gogithub.RegistrationToken {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == nil || c.token.GetToken() == "" {
		return nil
	}
	// request a new token if the existing one is about to expire in 5 minutes or less.
	if c.token.GetExpiresAt().Before(time.Now().Add(time.Minute * 5)) {
		c.token = nil
		return nil
	}
	return c.token
}

func (c *Repo) GetRegistrationToken(ctx context.Context) (*gogithub.RegistrationToken, error) {
	if token := c.existingToken(); token != nil {
		return token, nil
	}
	token, err := github.CreateRegistrationToken(ctx, c.owner, c.repo, c.opts...)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = &token
	return c.token, nil
}

func (c *Repo) GetWorkflowJob(ctx context.Context, jobID int64) (*gogithub.WorkflowJob, error) {
	job, err := github.GetWorkflowJob(ctx, c.owner, c.repo, jobID, c.opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow job %d for repository %s/%s: %w", jobID, c.owner, c.repo, err)
	}
	return &job, nil
}

type RepoClients struct {
	repos map[string]*Repo
	mu    sync.Mutex
}

func NewRepoClients() *RepoClients {
	return &RepoClients{
		repos: make(map[string]*Repo),
	}
}

func (rc *RepoClients) AddClient(owner, repo string, opts ...operations.Option) *Repo {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	key := owner + "/" + repo
	if existing, ok := rc.repos[key]; ok {
		return existing
	}
	newRepo := New(owner, repo, opts...)
	rc.repos[key] = newRepo
	return newRepo
}

func (rc *RepoClients) GetClient(owner, repo string) (*Repo, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	key := owner + "/" + repo
	existing, ok := rc.repos[key]
	return existing, ok
}

func (rc *RepoClients) GetClientFullName(fullName string) (*Repo, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	existing, ok := rc.repos[fullName]
	return existing, ok
}

func (rc *RepoClients) GetToken(ctx context.Context, owner, repo string) (*gogithub.RegistrationToken, error) {
	r, ok := rc.GetClient(owner, repo)
	if !ok {
		return nil, fmt.Errorf("no client for repository: '%s/%s'", owner, repo)
	}
	return r.GetRegistrationToken(ctx)
}

func (rc *RepoClients) GetTokenFullName(ctx context.Context, fullName string) (*gogithub.RegistrationToken, error) {
	r, ok := rc.GetClientFullName(fullName)
	if !ok {
		return nil, fmt.Errorf("no client for repository: '%s'", fullName)
	}
	return r.GetRegistrationToken(ctx)
}

func (rc *RepoClients) GetWorkflowJobFullName(ctx context.Context, fullName string, jobID int64) (*gogithub.WorkflowJob, error) {
	r, ok := rc.GetClientFullName(fullName)
	if !ok {
		return nil, fmt.Errorf("no client for repository: '%s'", fullName)
	}
	return r.GetWorkflowJob(ctx, jobID)
}

func (rc *RepoClients) RerunWorkflowJobFullName(ctx context.Context, fullName string, jobID int64) error {
	r, ok := rc.GetClientFullName(fullName)
	if !ok {
		return fmt.Errorf("no client for repository: '%s'", fullName)
	}
	return github.RerunWorkflowJob(ctx, r.owner, r.repo, jobID, r.opts...)
}

func (rc *RepoClients) CancelWorkflowRunFullName(ctx context.Context, fullName string, runID int64) error {
	r, ok := rc.GetClientFullName(fullName)
	if !ok {
		return fmt.Errorf("no client for repository: '%s'", fullName)
	}
	return github.CancelWorkflowRun(ctx, r.owner, r.repo, runID, r.opts...)
}

// EnsureJobCompletedOrCanceled checks the job status on GitHub. If the job is
// not yet completed or canceled, it requests cancellation of the workflow run
// and verifies that GitHub reports the job as completed/canceled.
func (rc *RepoClients) EnsureJobCompletedOrCanceled(ctx context.Context, fullName string, runID, jobID int64, jobName string) error {
	r, ok := rc.GetClientFullName(fullName)
	if !ok {
		return fmt.Errorf("no client for repository: '%s'", fullName)
	}
	return r.EnsureJobCompletedOrCanceled(ctx, runID, jobID, jobName)
}

func (c *Repo) EnsureJobCompletedOrCanceled(ctx context.Context, runID, jobID int64, jobName string) error {
	getJob := func(ctx context.Context) (*gogithub.WorkflowJob, error) {
		if jobID != 0 {
			job, err := c.GetWorkflowJob(ctx, jobID)
			if err == nil && job != nil {
				return job, nil
			}
		}
		if runID != 0 {
			scanner := github.NewJobsScanner(c.owner, c.repo, runID, "latest", 100, c.opts...)
			for scanner.Scan(ctx) {
				for _, j := range scanner.Response().Jobs {
					if jobID != 0 && j.GetID() == jobID {
						return j, nil
					}
					if jobName != "" && j.GetName() == jobName {
						return j, nil
					}
				}
			}
			if err := scanner.Err(); err != nil {
				return nil, err
			}
		}
		return nil, fmt.Errorf("job %q (id %d) not found in run %d for %s/%s", jobName, jobID, runID, c.owner, c.repo)
	}

	job, err := getJob(ctx)
	if err != nil {
		return fmt.Errorf("failed to get job status from GitHub: %w", err)
	}

	status := job.GetStatus()
	if status == "completed" {
		return nil
	}

	ctxlog.Info(ctx, "job not completed on GitHub, requesting cancellation",
		"owner", c.owner, "repo", c.repo,
		"run_id", runID, "job_id", job.GetID(),
		"current_status", status,
	)
	if err := github.CancelWorkflowRun(ctx, c.owner, c.repo, runID, c.opts...); err != nil {
		ctxlog.Warn(ctx, "cancel workflow run returned error; will verify if job completed", "error", err)
	}

	pollCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("timed out waiting for GitHub to cancel job %s (run %d)", jobName, runID)
		case <-ticker.C:
			j, err := getJob(pollCtx)
			if err != nil {
				continue
			}
			if j.GetStatus() == "completed" {
				ctxlog.Info(ctx, "verified job is completed/canceled on GitHub",
					"owner", c.owner, "repo", c.repo,
					"run_id", runID, "job_id", j.GetID(),
					"status", j.GetStatus(), "conclusion", j.GetConclusion(),
				)
				return nil
			}
		}
	}
}
