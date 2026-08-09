// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"

	"fmt"

	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/flags"
	"cloudeng.io/cmdutil/subcmd"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubclient"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubwebhook"
)

type CommonRunFlags struct {
	WaitForUserInput      bool `subcmd:"wait-for-user-input,,'if set VMs will not be released on exit until the user presses Enter, useful for debugging'"`
	DeleteAcquiredOnClose bool `subcmd:"delete-acquired-on-close,false,'if set VMs acquired from pools will be deleted on orchestrator shutdown, useful for debugging'"`
}

type RunCommand struct{}

type RunFlags struct {
	CommonRunFlags
	DeleteOrphanedVMs bool `subcmd:"delete-orphaned-vms,false,delete any orphaned VMs that are found at startup"`
}

func reconfigureVMPools(cfg Config, deleteAcquiredOnClose bool) Config {
	for name, poolCfg := range cfg.VMPools {
		poolCfg.DeleteAcquiredOnClose = deleteAcquiredOnClose
		cfg.VMPools[name] = poolCfg
	}
	return cfg
}

func (r RunCommand) Run(ctx context.Context, fl any, _ []string) error {
	fv := fl.(*RunFlags)
	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return fmt.Errorf("no config in context")
	}
	if cmdutil.IsExplicitlySet(subcmd.FlagSetFromContext(ctx).FlagSet(), "delete-acquired-on-close") {
		cfg = reconfigureVMPools(cfg, fv.DeleteAcquiredOnClose)
	}

	if cfg.Webhook.RelayURL == "" {
		return fmt.Errorf("no relay URL configured")
	}

	opts, err := cfg.Webhook.Options()
	if err != nil {
		return err
	}

	if fv.DeleteOrphanedVMs {
		sweepOrphanedVMs(ctx, cfg)
	}

	cq := githubclient.NewCompletionQueue(ctx,
		cfg.Global.CompletionQueueSize,
		cfg.Global.SuccessfulVMRetentionPeriod,
		cfg.Global.FailedVMRetentionPeriod)

	wh, err := githubclient.NewWorkflowEventHandler(ctx, cfg.Global.TmpDir, cq, cfg.VMPools, cfg.Repositories, repoClients)
	if err != nil {
		return err
	}
	//go wh.monitorCompletionQueue(ctx)

	defer func() {
		wh.DrainCompletionQueue(context.Background(), fv.WaitForUserInput)
		wh.Close(context.Background())
	}()

	h := githubwebhook.New(cfg.Webhook.RelayURL, wh.HandleWebhooks)
	return h.Listen(ctx, opts)
}

type RunJobFlags struct {
	GitHubFlags
	CommonRunFlags
	Labels flags.Commas `subcmd:"labels,,'labels to select the runner to use, may be repeated and may be comma separated'"`
}

func (r RunCommand) RunJob(ctx context.Context, flags any, _ []string) error {
	fv := flags.(*RunJobFlags)
	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return fmt.Errorf("no config in context")
	}
	if cmdutil.IsExplicitlySet(subcmd.FlagSetFromContext(ctx).FlagSet(), "delete-acquired-on-close") {
		cfg = reconfigureVMPools(cfg, fv.DeleteAcquiredOnClose)
	}

	rc, err := LookupRepositoryConfig(cfg, fv.GitHubFlags)
	if err != nil {
		return err
	}

	cq := githubclient.NewCompletionQueue(ctx,
		cfg.Global.CompletionQueueSize,
		cfg.Global.SuccessfulVMRetentionPeriod,
		cfg.Global.FailedVMRetentionPeriod)

	wh, err := githubclient.NewWorkflowEventHandler(ctx, cfg.Global.TmpDir, cq, cfg.VMPools, []githubclient.RepositoryConfig{rc}, repoClients)
	if err != nil {
		return err
	}
	defer wh.Close(context.Background())

	return wh.RunJob(ctx, rc.Service.Owner, rc.Service.Repo, fv.Labels.Values, fv.WaitForUserInput)
}
