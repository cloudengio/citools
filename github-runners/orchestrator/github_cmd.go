// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cloudeng.io/cmdutil/keys"
	"cloudeng.io/errors"
	"cloudeng.io/text/textutil"
	"cloudeng.io/webapi/clients/github/githubcmd"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubclient"
)

// maxBodyLines bounds how much of an error response body is included in the
// error message produced by detailErr.
const maxBodyLines = 5

// detailErr turns the (body, request, error) triple returned by the githubcmd
// scanner iterators into a single error. When available it appends the URL of
// the request that failed and the leading lines of the response body (truncated
// via textutil.Head) to make API failures (e.g. a 404) easier to debug.
func detailErr(body []byte, req *http.Request, err error) error {
	if err == nil {
		return nil
	}
	var location string
	if req != nil && req.URL != nil && req.URL.String() != "" {
		location = fmt.Sprintf(" [%s]", req.URL)
	}
	if detail := strings.TrimSpace(string(textutil.Head(body, '\n', maxBodyLines))); detail != "" {
		return fmt.Errorf("%w%s: %s", err, location, detail)
	}
	if location != "" {
		return fmt.Errorf("%w%s", err, location)
	}
	return err
}

// GitHubFlags are common flags for all github subcommands.
type GitHubFlags struct {
	Repo  string `subcmd:"repo,,GitHub repository name"`
	Owner string `subcmd:"owner,,GitHub repository owner (organization or user) name"`
}

type GitHubCommand struct{}

func LookupRepositoryConfig(cfg Config, flags GitHubFlags) (githubclient.RepositoryConfig, error) {
	repo := flags.Repo
	if repo == "" {
		return githubclient.RepositoryConfig{}, fmt.Errorf("--repo is required")
	}
	owner := flags.Owner
	for _, repoCfg := range cfg.Repositories {
		if repoCfg.Service.Repo == repo {
			if owner == "" || repoCfg.Service.Owner == owner {
				return repoCfg, nil
			}
		}
	}
	return githubclient.RepositoryConfig{}, fmt.Errorf("no matching repository configuration found")
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
	return detailErr(errf())
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
	return detailErr(errf())
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
	Runner string `subcmd:"runner,,'report only the jobs whose runner name starts with this value; runner names are a configured prefix followed by a generated suffix, so the prefix alone matches. Reports every job in the run if unset'"`
}

func (g GitHubCommand) GetJobConclusion(ctx context.Context, flags any, runIDArgs []string) error {

	fv := flags.(*GetJobConclusionFlags)
	gc, err := g.getCommand(ctx, fv.GitHubFlags)
	if err != nil {
		return err
	}

	// pending holds the runs still to be reported. Completion is tracked per
	// requested run, not per job, because a run contains any number of jobs.
	pending := make(map[int64]bool, len(runIDArgs))
	for _, idStr := range runIDArgs {
		var id int64
		_, err := fmt.Sscanf(idStr, "%d", &id)
		if err != nil {
			return fmt.Errorf("invalid run ID %q: %v", idStr, err)
		}
		pending[id] = true
	}

	statusVals := []string{"in_progress", "completed"}
	if fv.Status != "" {
		statusVals = []string{fv.Status}
	}

	for _, status := range statusVals {
		rfv := fv.ListRunsFlags
		rfv.Status = status
		runs, runsErr := gc.ListRuns(ctx, rfv)
		for run := range runs {
			runID := run.GetID()
			// Only the requested runs are worth a jobs request; a run already
			// reported under an earlier status is no longer pending.
			if !pending[runID] {
				continue
			}
			lfv := githubcmd.ListJobsFlags{
				Filter:   "latest",
				PageSize: fv.PageSize,
			}
			jobs, jobsErr := gc.ListJobs(ctx, lfv, runID)
			for job := range jobs {
				// An unset --runner is an empty prefix, matching every job.
				if !strings.HasPrefix(job.GetRunnerName(), fv.Runner) {
					continue
				}
				out, err := json.MarshalIndent(job, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(out))
			}
			if err := detailErr(jobsErr()); err != nil {
				return err
			}
			// The run's jobs have all been walked, so the run is done.
			delete(pending, runID)
			if len(pending) == 0 {
				return nil
			}
		}
		if err := detailErr(runsErr()); err != nil {
			return err
		}
	}
	return nil
}

// requestForEachID resolves the repository named by flags and applies request to
// each of the supplied IDs, reporting each one on stdout. noun names what the
// IDs identify ("job", "run") and action what is being asked of GitHub, both for
// use in messages. Every ID is attempted even if an earlier one fails, so that a
// single rejected request does not strand the rest; a malformed ID is a usage
// error and stops the command immediately.
func requestForEachID(ctx context.Context, flags GitHubFlags, noun, action string, idArgs []string, request func(context.Context, string, int64) error) error {
	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return fmt.Errorf("no config in context")
	}
	repoCfg, err := LookupRepositoryConfig(cfg, flags)
	if err != nil {
		return err
	}
	fullName := repoCfg.Service.Owner + "/" + repoCfg.Service.Repo

	var errs errors.M
	for _, idStr := range idArgs {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid %s ID %q: %v", noun, idStr, err)
		}
		if err := request(ctx, fullName, id); err != nil {
			errs.Append(fmt.Errorf("%s %s %d in %s: %w", action, noun, id, fullName, err))
			continue
		}
		fmt.Printf("%d\t%s\t%s requested\n", id, fullName, action)
	}
	return errs.Err()
}

type RerunJobFlags struct {
	GitHubFlags
}

// RerunJob requests that each of the supplied jobs, and the jobs depending on
// them, be rerun. GitHub queues the rerun asynchronously, so a job is reported
// here as requested rather than as started.
func (g GitHubCommand) RerunJob(ctx context.Context, flags any, jobIDArgs []string) error {
	fv := flags.(*RerunJobFlags)
	rc, ok := RepoClientsFromContext(ctx)
	if !ok {
		return fmt.Errorf("no repo clients in context")
	}
	if len(jobIDArgs) == 0 {
		return fmt.Errorf("at least one job ID is required")
	}
	return requestForEachID(ctx, fv.GitHubFlags, "job", "rerun", jobIDArgs,
		rc.RerunWorkflowJobFullName)
}

type CancelRunFlags struct {
	GitHubFlags
}

// CancelRun requests that each of the supplied workflow runs be cancelled,
// which cancels every job in the run. GitHub cancels asynchronously, so a run is
// reported here as requested rather than as stopped, and rejects the request
// with 409 Conflict if the run has already completed.
func (g GitHubCommand) CancelRun(ctx context.Context, flags any, runIDArgs []string) error {
	fv := flags.(*CancelRunFlags)
	rc, ok := RepoClientsFromContext(ctx)
	if !ok {
		return fmt.Errorf("no repo clients in context")
	}
	return requestForEachID(ctx, fv.GitHubFlags, "run", "cancel", runIDArgs,
		rc.CancelWorkflowRunFullName)
}

type CreateRegistrationTokenFlags struct {
	GitHubFlags
}

func (g GitHubCommand) CreateRegistrationToken(ctx context.Context, flags any, _ []string) error {
	fv := flags.(*CreateRegistrationTokenFlags)
	rc, ok := RepoClientsFromContext(ctx)
	if !ok {
		return fmt.Errorf("no repo clients in context")
	}
	gc, ok := rc.GetClient(fv.Owner, fv.Repo)
	if !ok {
		return fmt.Errorf("no GitHub client found for %s/%s", fv.Owner, fv.Repo)
	}
	tok, err := gc.GetRegistrationToken(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("%s\texpires=%s\n", tok.GetToken(), tok.GetExpiresAt().Format(time.RFC3339))
	return nil
}

type CreateWebhookFlags struct {
	GitHubFlags
	DeliveryURL string `subcmd:"delivery-url,,webhook delivery URL (required)"`
	SecretID    string `subcmd:"secret-id,,the keychain item name containing the webhook secret for HMAC signature verification"`
	SecretUser  string `subcmd:"secret-user,,the user associated with the keychain item containing the webhook secret"`
}

func (g GitHubCommand) CreateWebhook(ctx context.Context, flags any, _ []string) error {
	fv := flags.(*CreateWebhookFlags)
	gc, err := g.getCommand(ctx, fv.GitHubFlags)
	if err != nil {
		return err
	}
	fl := githubcmd.CreateWebhookFlags{
		URL:         fv.DeliveryURL,
		ContentType: "json",
		Events:      "workflow_job",
	}
	token, ok := keys.TokenFromContext(ctx, fv.SecretUser, fv.SecretID)
	if !ok {
		return fmt.Errorf("failed to get token from keychain")

	}
	defer token.Clear()

	hook, err := gc.CreateWebhook(ctx, string(token.Value()), fl)
	if err != nil {
		return err
	}
	fmt.Printf("%d\t%s\tactive=%v\t%s\n",
		hook.GetID(), hook.GetConfig().GetURL(), hook.GetActive(), strings.Join(hook.GetEvents(), ","))
	return nil
}
