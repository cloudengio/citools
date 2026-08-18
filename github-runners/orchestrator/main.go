// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/cmdyaml"
	"cloudeng.io/cmdutil/keys"
	"cloudeng.io/cmdutil/subcmd"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/webapi/clients/github/githubcmd"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubclient"
)

const CmdSpecYAML = `
name: github-runner-orchestrator
summary: orchestrator for GitHub self-hosted runners
commands:
  - name: run
    summary: run the orchestrator
  - name: run-job
    summary: run a single job on a VM, useful for testing vms
  - name: github
    summary: GitHub API commands
    commands:
      - name: list-runners
        summary: list self-hosted runners for a repository
      - name: list-runs
        summary: list workflow runs for a repository
      - name: get-runs
        summary: get workflow runs for a repository
        args:
           - <run_id> (one or more run IDs to retrieve)
           - ...
      - name: create-registration-token
        summary: create a runner registration token for a repository
      - name: create-webhook
        summary: create a webhook for a repository
      - name: runner-job-conclusion
        summary: print the conclusion of the job assigned to a named runner
        args:
          - <run_id> (one or more run IDs to check for the runner's job)
          - ...
      - name: rerun-job
        summary: request that one or more workflow jobs, and the jobs that depend on them, be rerun
        args:
          - <job_id> (one or more job IDs to rerun)
          - ...
      - name: cancel-run
        summary: request that one or more workflow runs, and all of their jobs, be cancelled
        args:
          - <run_id> (one or more run IDs to cancel)
          - ...
  - name: install
    summary: write the bundled minimal orchestrator config file to a standard location
  - name: bundle
    summary: build a signed macOS .app bundle that installs the orchestrator
  - name: service
    summary: manage the orchestrator as a per-user launchd login service
    commands:
      - name: install
        summary: install and load the login service (LaunchAgent)
      - name: uninstall
        summary: unload and remove the login service
      - name: status
        summary: show the login service status
      - name: restart
        summary: restart the login service
  - name: webapp-build
    summary: build the embedded web UI frontend (runs npm install, gen and build)
  - name: config
    summary: config related commands
    commands:
      - name: show
        summary: show the current configuration
      - name: describe
        summary: describe the configuration spec
  - name: vms
    summary: inspect and clean up the VMs created by the orchestrator's pools
    commands:
      - name: list
        summary: list the VMs created by the orchestrator's configured pools
      - name: delete
        summary: delete the VMs created by the orchestrator's configured pools
`

type GlobalFlags struct {
	ConfigFile string `subcmd:"config,github_orchestrator_config.yml,path to YAML configuration file" doc:"path to YAML configuration file"`
	cmdutil.LoggingFlags
	Verbose bool `subcmd:"verbose,false,enable verbose logging" doc:"enable verbose logging"`
}

func createCLI() *subcmd.CommandSetYAML {
	cmdSet := subcmd.MustFromYAML(CmdSpecYAML)

	runCmd := RunCommand{}
	cmdSet.Set("run").MustRunner(runCmd.Run, &RunFlags{})
	cmdSet.Set("run-job").MustRunner(runCmd.RunJob, &RunJobFlags{})

	installCmd := InstallCommand{}
	cmdSet.Set("install").MustRunner(installCmd.Run, &InstallFlags{})

	bundleCmd := BundleCommand{}
	cmdSet.Set("bundle").MustRunner(bundleCmd.Run, &BundleFlags{})

	serviceCmd := ServiceCommand{}
	cmdSet.Set("service", "install").MustRunner(serviceCmd.Install, &ServiceInstallFlags{})
	cmdSet.Set("service", "uninstall").MustRunner(serviceCmd.Uninstall, &struct{}{})
	cmdSet.Set("service", "status").MustRunner(serviceCmd.Status, &struct{}{})
	cmdSet.Set("service", "restart").MustRunner(serviceCmd.Restart, &struct{}{})

	webappCmd := WebappBuildCommand{}
	cmdSet.Set("webapp-build").MustRunner(webappCmd.Run, &WebappBuildFlags{})

	cfgCmd := ConfigCommand{}
	cmdSet.Set("config", "show").MustRunner(cfgCmd.Show, &struct{}{})
	cmdSet.Set("config", "describe").MustRunner(cfgCmd.Describe, &struct{}{})

	vmCmd := VMCommand{}
	cmdSet.Set("vms", "list").MustRunner(vmCmd.List, &VMListFlags{})
	cmdSet.Set("vms", "delete").MustRunner(vmCmd.Delete, &VMDeleteFlags{})

	ghCmd := GitHubCommand{}
	cmdSet.Set("github", "list-runners").MustRunner(ghCmd.ListRunners, &ListRunnersFlags{})
	cmdSet.Set("github", "list-runs").MustRunner(ghCmd.ListRuns, &ListRunsFlags{})
	cmdSet.Set("github", "get-runs").MustRunner(ghCmd.GetRuns, &GetRunsFlags{})
	//cmdSet.Set("github", "queued-runs").MustRunner(ghCmd.QueuedRuns, &QueuedRunsFlags{})
	cmdSet.Set("github", "create-webhook").MustRunner(ghCmd.CreateWebhook, &CreateWebhookFlags{})
	cmdSet.Set("github", "create-registration-token").MustRunner(ghCmd.CreateRegistrationToken, &CreateRegistrationTokenFlags{})
	cmdSet.Set("github", "runner-job-conclusion").MustRunner(ghCmd.GetJobConclusion, &GetJobConclusionFlags{})
	cmdSet.Set("github", "rerun-job").MustRunner(ghCmd.RerunJob, &RerunJobFlags{})
	cmdSet.Set("github", "cancel-run").MustRunner(ghCmd.CancelRun, &CancelRunFlags{})

	return cmdSet
}

var globalFlags GlobalFlags
var repoClients *githubclient.RepoClients

func verbose() bool {
	return globalFlags.Verbose
}

// ensureToolPath prepends the standard Homebrew bin directories to PATH (if they
// exist and are not already present), so tools the orchestrator invokes — tart,
// go, docker — are found even when it is launched from a .app or by launchd,
// which provide only a minimal PATH.
func ensureToolPath() {
	path := os.Getenv("PATH")
	var prepend []string
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if strings.Contains(":"+path+":", ":"+d+":") {
			continue
		}
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			prepend = append(prepend, d)
		}
	}
	if len(prepend) > 0 {
		_ = os.Setenv("PATH", strings.Join(prepend, ":")+":"+path)
	}
}

func main() {
	ensureToolPath()
	ctx := context.Background()
	ctx, cancel := context.WithCancelCause(ctx)
	cli := createCLI()
	fs := subcmd.NewFlagSet().MustRegisterFlagStruct(&globalFlags, nil, nil)
	cli.WithGlobalFlags(fs)
	var logCloser func() error
	defer func() {
		if logCloser != nil {
			if err := logCloser(); err != nil {
				fmt.Fprintln(os.Stderr, "error closing logger:", err)
			}
		}
	}()
	ks := keys.NewInMemoryKeyStore()
	ctx = keys.ContextWithKeyStore(ctx, ks)
	cli.WithMain(func(ctx context.Context, cmdRunner func(ctx context.Context) error) error {
		// Bootstrap/tooling commands need no orchestrator configuration, keychain
		// access or GitHub clients: webapp-build and bundle are build tools, and
		// install generates the config file itself. Skip that setup for them.
		if isWebappBuildInvocation() || isInstallInvocation() || isBundleInvocation() || isServiceInvocation() {
			return cmdRunner(ctx)
		}
		var cfg Config
		if err := cmdyaml.ParseConfigFilesStrict(ctx, &cfg, globalFlags.ConfigFile); err != nil {
			return fmt.Errorf("failed to read the configuration file %q: %v\nedit that file to correct it", globalFlags.ConfigFile, err)
		}
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("the configuration file %q is invalid: %v\nedit that file to correct it", globalFlags.ConfigFile, err)
		}
		loggerConfig := cfg.Logging.WithFlagOverrides(
			fs.FlagSet(), globalFlags.LoggingFlags)
		opts := loggerConfig.Options()
		logger, err := loggerConfig.NewLoggerOpts(opts)
		if err != nil {
			return fmt.Errorf("error setting up logger: %v", err)
		}
		ctx = ctxlog.WithLogger(ctx, logger.Logger)
		ctx = ContextWithConfig(ctx, cfg)
		logCloser = logger.Close
		if len(cfg.ICloudKeychain.Items) > 0 {
			ctx, err = loadKeychain(ctx, cfg.ICloudKeychain)
			if err != nil {
				return fmt.Errorf("error loading keychain: %v", err)
			}
		}
		rc, err := createRepoClients(cfg)
		if err != nil {
			return fmt.Errorf("error creating GitHub clients: %v", err)
		}
		repoClients = rc
		return cmdRunner(ctx)
	})
	// Handle SIGTERM as well as SIGINT so the orchestrator shuts down cleanly
	// (draining/deleting VMs) when launchd stops the login service.
	cmdutil.HandleSignals(func() { cancel(cmdutil.ErrInterrupt) }, os.Interrupt, syscall.SIGTERM)
	if err := cli.Dispatch(ctx); err != nil {
		if errors.Is(err, cmdutil.ErrInterrupt) {
			return
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		// Exit non-zero so callers (the launcher, shells, CI) can detect the
		// failure. os.Exit skips deferred cleanup, so flush the logger first.
		if logCloser != nil {
			_ = logCloser()
			logCloser = nil
		}
		os.Exit(1)
	}
}

func loadKeychain(ctx context.Context, cfg ICloudKeychainConfig) (context.Context, error) {
	ims, ok := keys.KeyStoreFromContext(ctx)
	if !ok {
		return ctx, fmt.Errorf("no keystore in context")
	}
	fs, err := cfg.FS(false)
	if err != nil {
		return ctx, fmt.Errorf("error creating keychain fs: %v", err)
	}
	for _, item := range cfg.Items {
		if err := ims.ReadYAML(ctx, fs, item); err != nil {
			return ctx, fmt.Errorf("error reading keychain item %q: %v", item, err)
		}
		if verbose() {
			ctxlog.Info(ctx, "loaded keychain item", "item", item, "total items", ims.Len())
		}
	}
	if verbose() {
		for _, key := range ims.KeySpecs() {
			ctxlog.Info(ctx, "keychain item", "user", key.User, "id", key.ID)
		}
	}
	return ctx, nil
}

func createRepoClients(cfg Config) (*githubclient.RepoClients, error) {
	for _, repo := range cfg.Repositories {
		if repo.Service.Repo == "" {
			return nil, fmt.Errorf("repository name is required for each repository in config")
		}
		if repo.Service.Owner == "" {
			return nil, fmt.Errorf("repository owner is required for each repository in config")
		}
	}
	clients := githubclient.NewRepoClients()
	for _, repo := range cfg.Repositories {
		opts, err := githubcmd.OptionsForEndpoint(repo.Crawl)
		if err != nil {
			return nil, fmt.Errorf("error creating GitHub client for %s/%s: %v", repo.Service.Owner, repo.Service.Repo, err)
		}
		clients.AddClient(repo.Service.Owner, repo.Service.Repo, opts...)
	}
	return clients, nil
}
