// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cloudeng.io/cmdutil/keys"
	"cloudeng.io/webapi/clients/github/githubcmd"
)

// GitHubFlags are common flags for all github subcommands.
type GitHubFlags struct {
	Repo  string `subcmd:"repo,,GitHub repository name"`
	Owner string `subcmd:"owner,,GitHub repository owner (organization or user) name"`
}

type GitHubCommand struct{}

func LookupRepositoryConfig(cfg Config, flags GitHubFlags) (RepositoryConfig, error) {
	repo := flags.Repo
	if repo == "" {
		return RepositoryConfig{}, fmt.Errorf("--repo is required")
	}
	owner := flags.Owner
	for _, repoCfg := range cfg.Repositories {
		if repoCfg.Service.Repo == repo {
			if owner == "" || repoCfg.Service.Owner == owner {
				return repoCfg, nil
			}
		}
	}
	return RepositoryConfig{}, fmt.Errorf("no matching repository configuration found")
}

type ListRunnersFlags struct {
	GitHubFlags
	githubcmd.ListRunnersFlags
}

func (g GitHubCommand) getCommand(ctx context.Context, flags GitHubFlags) (*githubcmd.Command, error) {
	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("no config in context")
	}
	repoCfg, err := LookupRepositoryConfig(cfg, flags)
	if err != nil {
		return nil, err
	}
	gc, err := githubcmd.NewCommand(ctx, repoCfg.Crawl)
	if err != nil {
		return nil, err
	}
	return gc, nil
}

func (g GitHubCommand) ListRunners(ctx context.Context, flags any, _ []string) error {
	fv := flags.(*ListRunnersFlags)
	gc, err := g.getCommand(ctx, fv.GitHubFlags)
	if err != nil {
		return err
	}
	runners, errf := gc.ListRunners(ctx, fv.ListRunnersFlags)
	for runner := range runners {
		out, err := json.MarshalIndent(runner, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
	}
	return errf()
}

type ListRunsFlags struct {
	GitHubFlags
	githubcmd.ListRunsFlags
}

func (g GitHubCommand) ListRuns(ctx context.Context, flags any, _ []string) error {
	fv := flags.(*ListRunsFlags)
	gc, err := g.getCommand(ctx, fv.GitHubFlags)
	if err != nil {
		return err
	}
	runs, errf := gc.ListRuns(ctx, fv.ListRunsFlags)
	for run := range runs {
		out, err := json.MarshalIndent(run, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
	}
	return errf()
}

type GetRunsFlags struct {
	GitHubFlags
}

func (g GitHubCommand) GetRuns(ctx context.Context, flags any, args []string) error {
	fv := flags.(*GetRunsFlags)
	gc, err := g.getCommand(ctx, fv.GitHubFlags)
	if err != nil {
		return err
	}
	for run, err := range gc.GetRuns(ctx, args) {
		if err != nil {
			return err
		}
		out, err := json.MarshalIndent(run, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
	}
	return nil
}

type GetJobConclusionFlags struct {
	GitHubFlags
	githubcmd.ListRunsFlags
}

func (g GitHubCommand) GetJobConclusion(ctx context.Context, flags any, runIDArgs []string) error {

	fv := flags.(*GetJobConclusionFlags)
	gc, err := g.getCommand(ctx, fv.GitHubFlags)
	if err != nil {
		return err
	}

	runIDs := make([]int64, len(runIDArgs))
	for i, idStr := range runIDArgs {
		var id int64
		_, err := fmt.Sscanf(idStr, "%d", &id)
		if err != nil {
			return fmt.Errorf("invalid run ID %q: %v", idStr, err)
		}
		runIDs[i] = id
	}

	statusVals := []string{"in_progress", "completed"}
	if fv.Status != "" {
		statusVals = []string{fv.Status}
	}

	jobDone := make(map[int64]bool)

	for _, status := range statusVals {
		rfv := fv.ListRunsFlags
		rfv.Status = status
		runs, runsErr := gc.ListRuns(ctx, rfv)
		for run := range runs {
			lfv := githubcmd.ListJobsFlags{
				Filter:   "latest",
				PageSize: fv.PageSize,
			}
			jobs, jobsErr := gc.ListJobs(ctx, lfv, run.ID)
			for job := range jobs {
				for _, id := range runIDs {
					if job.RunID == int64(id) && !jobDone[job.ID] {
						out, err := json.MarshalIndent(job, "", "  ")
						if err != nil {
							return err
						}
						fmt.Println(string(out))
						jobDone[job.ID] = true
						if len(jobDone) >= len(runIDs) {
							return nil
						}
					}
				}
			}
			if err := jobsErr(); err != nil {
				return err
			}
		}
		if err := runsErr(); err != nil {
			return err
		}
	}
	return nil
}

type CreateRegistrationTokenFlags struct {
	GitHubFlags
}

func (g GitHubCommand) CreateRegistrationToken(ctx context.Context, flags any, _ []string) error {
	fv := flags.(*CreateRegistrationTokenFlags)
	gc, err := g.getCommand(ctx, fv.GitHubFlags)
	if err != nil {
		return err
	}
	tok, err := gc.CreateRegistrationToken(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("%s\texpires=%s\n", tok.Token, tok.ExpiresAt.Format(time.RFC3339))
	return nil
}

type CreateWebhookFlags struct {
	GitHubFlags
	WebhookName string `subcmd:"webhook-name,,name of the webhook configuration to use"`
}

func (g GitHubCommand) CreateWebhook(ctx context.Context, flags any, _ []string) error {
	fv := flags.(*CreateWebhookFlags)

	gc, err := g.getCommand(ctx, fv.GitHubFlags)
	if err != nil {
		return err
	}
	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return fmt.Errorf("no config in context")
	}

	whCfg, ok := cfg.Webhooks[fv.WebhookName]
	if !ok {
		return fmt.Errorf("no webhook configuration found for %q", fv.WebhookName)
	}

	fl := githubcmd.CreateWebhookFlags{
		URL:         whCfg.DeliveryURL,
		ContentType: "json",
		Events:      strings.Join(whCfg.Events, ","),
	}
	token, ok := keys.TokenFromContext(ctx, whCfg.SecretUser, whCfg.SecretID)
	if !ok {
		return fmt.Errorf("failed to get token from keychain")

	}
	defer token.Clear()

	hook, err := gc.CreateWebhook(ctx, token.String(), fl)
	if err != nil {
		return err
	}
	fmt.Printf("%d\t%s\tactive=%v\t%s\n",
		hook.ID, hook.Config.URL, hook.Active, strings.Join(hook.Events, ","))
	return nil
}
