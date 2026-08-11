// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/flags"
	"cloudeng.io/cmdutil/subcmd"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/webapp/webassets"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubclient"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubwebhook"
	"github.com/cloudengio/citools/runners/macos/orchestrator/webui"
)

// startWebUI starts the management web UI / JSON API HTTP server if it is
// enabled in the configuration, returning the server so the caller can shut it
// down. It returns nil when the web UI is disabled. The server is started before
// the rest of initialization completes; the backend's handler is wired in later
// (via SetHandler) so pool/workflow data fills in as it becomes available.
func startWebUI(ctx context.Context, cfg Config, backend *webuiBackend) *http.Server {
	if !cfg.WebUI.Enabled || cfg.WebUI.ListenAddress == "" {
		return nil
	}
	assetOpts := cfg.WebUI.Reload.Options()
	if len(assetOpts) > 0 {
		assetOpts = append(assetOpts, webassets.WithLogger(ctxlog.Logger(ctx)))
		ctxlog.Info(ctx, "web ui asset reloading enabled", "reload_root", cfg.WebUI.Reload.ReloadRoot)
	}
	server := webui.NewServer(backend, webui.WithAssets(webui.FrontendAssets(assetOpts...)))
	srv := &http.Server{
		Addr:              cfg.WebUI.ListenAddress,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Derive every request context from the app context so that cancelling
		// it (e.g. on Ctrl-C) also cancels in-flight handlers, including the
		// long-lived /events SSE stream. Without this, Shutdown would block
		// forever waiting for an open SSE connection to become idle.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		ctxlog.Info(ctx, "starting web ui", "address", cfg.WebUI.ListenAddress)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			ctxlog.Error(ctx, "web ui server error", "error", err)
		}
	}()
	return srv
}

// shutdownWebUI stops the web UI server, giving in-flight requests a short grace
// period to drain before force-closing any that remain (e.g. a slow SSE client),
// so Ctrl-C always exits promptly.
func shutdownWebUI(srv *http.Server) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
	}
}

type CommonRunFlags struct {
	WaitForUserInput      bool `subcmd:"wait-for-user-input,,'if set VMs will not be released on exit until the user presses Enter, useful for debugging'"`
	DeleteAcquiredOnClose bool `subcmd:"delete-acquired-on-close,false,'if set VMs acquired from pools will be deleted on orchestrator shutdown, useful for debugging'"`
}

type RunCommand struct{}

type RunFlags struct {
	CommonRunFlags
	DeleteOrphanedVMs bool `subcmd:"delete-orphaned-vms,false,delete any orphaned VMs that are found at startup"`
}

// statusRetention returns how long completed workflow records should be kept in
// the web UI status tracker: at least as long as the longer of the two VM
// retention periods, so a finished job stays visible while its VM lingers.
func statusRetention(cfg Config) time.Duration {
	return max(cfg.Global.SuccessfulVMRetentionPeriod, cfg.Global.FailedVMRetentionPeriod)
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

	// Start the web UI immediately so it is available (serving config and an
	// empty-but-live dashboard) while the slower parts of initialization below
	// proceed; the handler is wired in once ready and the UI fills in.
	backend := newWebUIBackend(cfg, globalFlags.ConfigFile)
	webUIStarted := false
	if srv := startWebUI(ctx, cfg, backend); srv != nil {
		webUIStarted = true
		defer shutdownWebUI(srv)
	}

	if fv.DeleteOrphanedVMs {
		sweepOrphanedVMs(ctx, cfg)
	}

	cq := githubclient.NewCompletionQueue(ctx,
		cfg.Global.CompletionQueueSize,
		cfg.Global.SuccessfulVMRetentionPeriod,
		cfg.Global.FailedVMRetentionPeriod)

	wh, err := githubclient.NewWorkflowEventHandler(ctx, cfg.Global.TmpDir, cq, statusRetention(cfg), cfg.VMPools, cfg.Repositories, repoClients)
	if err != nil {
		return err
	}

	defer func() {
		wh.DrainCompletionQueue(context.Background(), fv.WaitForUserInput)
		wh.Close(context.Background())
	}()

	// Wire the handler into the already-running web UI; pool and workflow data
	// now becomes available and early SSE clients are notified to refresh.
	if webUIStarted {
		backend.SetHandler(ctx, wh)
	}

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

	wh, err := githubclient.NewWorkflowEventHandler(ctx, cfg.Global.TmpDir, cq, statusRetention(cfg), cfg.VMPools, []githubclient.RepositoryConfig{rc}, repoClients)
	if err != nil {
		return err
	}
	defer wh.Close(context.Background())

	return wh.RunJob(ctx, rc.Service.Owner, rc.Service.Repo, fv.Labels.Values, fv.WaitForUserInput)
}
