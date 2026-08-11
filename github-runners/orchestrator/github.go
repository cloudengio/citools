// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

/*
// GitHub provides access to GitHub Actions APIs for a specific repository.
type GitHub struct {
	token_id string
	owner    string
	repo     string
	verbose  bool
}

func (g *GitHub) operationOptions(ctx context.Context, op string) []operations.Option {
	if g.verbose {
		ctxlog.Logger(ctx).Info("GitHub operation", "operation", op, "owner", g.owner, "repo", g.repo, "token_id", g.token_id)
	}
	ops := []operations.Option{
		operations.WithAuth(githubclient.BearerToken{KeyID: g.token_id}),
	}
	if g.verbose {
		ops = append(ops, operations.WithLogger(ctxlog.Logger(ctx).With("github", op)))
	}
	return ops
}

// GetRunners returns all self-hosted runners registered for the repository.
func (g *GitHub) GetRunners(ctx context.Context) ([]githubclient.Runner, error) {
	sc := githubclient.NewRunnersScanner(g.owner, g.repo, 100, g.operationOptions(ctx, "get-runners")...)
	var runners []githubclient.Runner
	for sc.Scan(ctx) {
		runners = append(runners, sc.Response().Runners...)
	}
	return runners, sc.Err()
}

// CreateRegistrationToken creates a new runner registration token for the repository.
func (g *GitHub) CreateRegistrationToken(ctx context.Context) (string, error) {
	tok, err := githubclient.CreateRegistrationToken(ctx, g.owner, g.repo, g.operationOptions(ctx, "create-registration-token")...)
	if err != nil {
		return "", err
	}
	return tok.Token, nil
}



// GetQueuedRunCount returns the number of queued workflow runs for the repository.
func (g *GitHub) GetQueuedRunCount(ctx context.Context) (int, error) {
	sc := githubclient.NewRunsScanner(g.owner, g.repo, 1,
		githubclient.RunsFilter{Status: "queued"}, g.operationOptions(ctx, "get-queued-run-count")...)
	if !sc.Scan(ctx) {
		return 0, sc.Err()
	}
	return sc.Response().TotalCount, sc.Err()
}

/*
// GetRunnerJobConclusion searches recent in-progress and completed runs for a job
// assigned to runnerName and returns its conclusion.
func (g *GitHub) GetRunnerJobConclusion(ctx context.Context, runIDs ...int) ([]githubclient.Job, error) {
	found := []githubclient.Job{}
	opts := g.operationOptions(ctx, "get-runner-job-conclusion")
	for _, status := range []string{"in_progress", "completed"} {
		sc := githubclient.NewRunsScanner(g.owner, g.repo, 20,
			githubclient.RunsFilter{Status: status}, opts...)
		for sc.Scan(ctx) {
			for _, run := range sc.Response().WorkflowRuns {
				jsc := githubclient.NewJobsScanner(g.owner, g.repo, run.ID, "latest", 50, opts...)
				for jsc.Scan(ctx) {
					for _, job := range jsc.Response().Jobs {
						for _, id := range runIDs {
							if job.RunID == int64(id) {
								found = append(found, job)
								if len(found) >= len(runIDs) {
									return found, nil
								}
							}
						}
					}
				}
				if err := jsc.Err(); err != nil {
					return nil, err
				}
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not find job for runs %v", runIDs)
}
*/
