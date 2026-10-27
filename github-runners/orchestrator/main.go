// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"

	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/cmdconfig"
	"cloudeng.io/cmdutil/subcmd"
	"cloudeng.io/macos/macosutils"
	"cloudeng.io/webapi/clients/github/githubcmd"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubclient"
)

const CmdSpecYAML = `
name: github-runner-orchestrator
summary: orchestrator for GitHub self-hosted runners
commands:
  - name: run
    summary: run the orchestrator
  - name: launch
    summary: launch the orchestrator as a GUI application
  - name: view
    summary: view the web UI of an orchestrator running on a remote host via SSH port forwarding
    args:
      - <[local-port:]host[:remote-port]> (remote host running the orchestrator, with optional local and remote ports)
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
  - name: version
    summary: print the version and git revision of this binary
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
  {{range subcmdExtension "config"}}{{.}}
  {{end}}
  - name: vms
    summary: inspect and clean up the VMs created by the orchestrator's pools
    commands:
      - name: list
        summary: list the VMs created by the orchestrator's configured pools
      - name: delete
        summary: delete the VMs created by the orchestrator's configured pools
  - name: jwt
    summary: JSON Web Token (JWT) key and authentication commands
    commands:
      - name: create
        summary: |
          create a new Ed25519 JWT signing key pair, if a <filename>
          is specified the private key is written to <filename> in json format
          and the public key to <filename>.pub.json. If filename is - for stdout
          then the public key is written to <keyuser>-<keyid>.pub.json:
        args:
          - <filename> (the private key file, or - for stdou)
      - name: issue
        summary: serve a JWT authentication cookie to trusted clients via a random URL
`

type GlobalFlags struct {
	Config cmdconfig.FilesFlag `subcmd:"config,'config.yaml,config.env','comma separated list of the YAML configuration file and of the file of NAME=VALUE environment variable definitions (.env) for use in it, replacing the defaults; searched for in the current directory, ~/Library/Application Support/io.cloudeng.github-runner-orchestrator on macOS and ~/.config/github-orchestrator; the shell environment overrides the values the latter defines'" doc:"the configuration file and the file of environment variable definitions for use in it"`
	cmdutil.LoggingFlags
	Verbose bool `subcmd:"verbose,false,enable verbose logging" doc:"enable verbose logging"`
}

func createCLI() *subcmd.CommandSetYAML {
	configCmds := cmdconfig.ConfigCommands[Config]{Summary: "root configuration options\n"}
	cmdSet := subcmd.MustFromYAMLTemplate(CmdSpecYAML, cmdconfig.ConfigExtension(configCmds))
	cmdSet.MustAddExtensions()

	runCmd := RunCommand{}
	cmdSet.Set("run").MustRunner(runCmd.Run, &RunFlags{})
	cmdSet.Set("run-job").MustRunner(runCmd.RunJob, &RunJobFlags{})

	vmCmd := VMCommand{}
	cmdSet.Set("vms", "list").MustRunner(vmCmd.List, &VMListFlags{})
	cmdSet.Set("vms", "delete").MustRunner(vmCmd.Delete, &VMDeleteFlags{})

	ghCmd := GitHubCommand{}
	cmdSet.Set("github", "list-runners").MustRunner(ghCmd.ListRunners, &ListRunnersFlags{})
	cmdSet.Set("github", "list-runs").MustRunner(ghCmd.ListRuns, &ListRunsFlags{})
	cmdSet.Set("github", "get-runs").MustRunner(ghCmd.GetRuns, &GetRunsFlags{})
	cmdSet.Set("github", "create-webhook").MustRunner(ghCmd.CreateWebhook, &CreateWebhookFlags{})
	cmdSet.Set("github", "create-registration-token").MustRunner(ghCmd.CreateRegistrationToken, &CreateRegistrationTokenFlags{})
	cmdSet.Set("github", "runner-job-conclusion").MustRunner(ghCmd.GetJobConclusion, &GetJobConclusionFlags{})
	cmdSet.Set("github", "rerun-job").MustRunner(ghCmd.RerunJob, &RerunJobFlags{})
	cmdSet.Set("github", "cancel-run").MustRunner(ghCmd.CancelRun, &CancelRunFlags{})

	// The following commands need the orchestrator configuration,
	// so they have a pre-hook that loads it and sets up the context.
	cmdSet.Set("run").MustSetPreHooks(configPrehook, withKeysPrehook, repoClientsPrehook)
	cmdSet.Set("run-job").MustSetPreHooks(configPrehook, withKeysPrehook, repoClientsPrehook)
	cmdSet.Set("github").MustSetPreHooks(configPrehook, withKeysPrehook, repoClientsPrehook)
	cmdSet.Set("vms").MustSetPreHooks(configPrehook, withKeysPrehook)
	cmdSet.Set("config").MustSetPreHooks(configPrehook)

	// version needs no configuration, so it has no pre-hook.
	cmdSet.Set("version").MustRunner(VersionCommand{}.Run, &struct{}{})

	launchCmd := LaunchCommand{}
	cmdSet.Set("launch").MustRunner(launchCmd.Run, &struct{}{})

	viewCmd := ViewCommand{}
	cmdSet.Set("view").MustRunner(viewCmd.Run, &ViewFlags{})

	installCmd := InstallCommand{}
	cmdSet.Set("install").MustRunner(installCmd.Run, &InstallFlags{})

	bundleCmd := BundleCommand{}
	cmdSet.Set("bundle").MustRunner(bundleCmd.Run, &BundleFlags{})

	serviceCmd := ServiceCommand{}
	cmdSet.Set("service", "install").MustRunner(serviceCmd.Install, &ServiceInstallFlags{})
	cmdSet.Set("service", "uninstall").MustRunner(serviceCmd.Uninstall, &ServiceFlags{})
	cmdSet.Set("service", "status").MustRunner(serviceCmd.Status, &ServiceFlags{})
	cmdSet.Set("service", "restart").MustRunner(serviceCmd.Restart, &ServiceFlags{})

	webappCmd := WebappBuildCommand{}
	cmdSet.Set("webapp-build").MustRunner(webappCmd.Run, &WebappBuildFlags{})

	jwtCmd := JWTCommand{}
	cmdSet.Set("jwt", "create").MustRunner(jwtCmd.Create, &JWTCreateFlags{})
	cmdSet.Set("jwt", "issue").MustRunner(jwtCmd.Issuer, &JWTIssuerFlags{})
	cmdSet.Set("jwt", "issue").MustSetPreHooks(configPrehook, withKeysPrehook)

	return cmdSet
}

var globalFlags GlobalFlags

func verbose() bool {
	return globalFlags.Verbose
}

// VerboseFlags is embedded by commands that run a sequence of steps, so that
// --verbose can be given either globally, before the subcommand, or on the
// subcommand itself.
type VerboseFlags struct {
	Verbose bool `subcmd:"verbose,false,print each step as it is run"`
}

// stepsVerbose reports whether steps should be printed, honouring the global
// --verbose as well as the subcommand's own.
func (vf VerboseFlags) stepsVerbose() bool {
	return vf.Verbose || verbose()
}

// loggerPrehook creates the logger from the configuration, as overridden by the
// command line flags.
var loggerPrehook = subcmd.LoggerHook("withLoggerPrehook", func(ctx context.Context, fs *flag.FlagSet) (*cmdutil.Logger, error) {
	cfg, _ := cmdutil.ContextValue[Config](ctx)
	loggerConfig := cfg.Logging.WithFlagOverrides(fs, globalFlags.LoggingFlags)
	return loggerConfig.NewLoggerOpts(loggerConfig.Options(), cfg.Logging.Options()...)
})

func configPrehook(ctx context.Context) (context.Context, string, subcmd.PostHook, error) {
	hook := cmdconfig.ConfigHook[Config]{
		ID:       "withConfigPrehook",
		Files:    &globalFlags.Config,
		Spec:     ConfigSpec(),
		Required: true,
	}
	ctx, id, _, err := hook.Run(ctx)
	if err != nil {
		return ctx, id, nil, err
	}
	ctx, _, postHook, err := loggerPrehook(ctx)
	return ctx, id, postHook, err
}

func repoClientsPrehook(ctx context.Context) (context.Context, string, subcmd.PostHook, error) {
	id := "withRepoClientsPrehook"
	postHook := func(ctx context.Context) (string, error) { return id, nil }
	cfg, ok := cmdutil.ContextValue[Config](ctx)
	if !ok {
		return ctx, id, postHook, fmt.Errorf("no config in context")
	}
	rc, err := createRepoClients(cfg)
	if err != nil {
		return ctx, id, postHook, fmt.Errorf("error creating GitHub clients: %v", err)
	}
	ctx = cmdutil.ContextWithValue(ctx, rc)
	return ctx, id, postHook, nil
}

func main() {
	if runtime.GOOS == "darwin" {
		runtime.LockOSThread() // Required for AppKit / Cocoa UI on macOS
	}
	if len(os.Args) == 1 {
		if _, ok := macosutils.ProcessInBundle(); ok {
			os.Args = append(os.Args, "launch")
		}
	}
	ctx := context.Background()
	ctx, cancel := context.WithCancelCause(ctx)
	cli := createCLI()
	fs := subcmd.NewFlagSet().MustRegisterFlagStruct(&globalFlags, nil, nil)
	cli.WithGlobalFlags(fs)

	// Handle SIGTERM as well as SIGINT so the orchestrator shuts down cleanly
	// (draining/deleting VMs) when launchd stops the login service.
	cmdutil.HandleSignals(func() { cancel(cmdutil.ErrInterrupt) }, os.Interrupt, syscall.SIGTERM)
	if err := cli.Dispatch(ctx); err != nil {
		if isCleanShutdown(err) {
			return
		}
		// This error may have occurred before slog is configured/enabled so
		// log it to stderr with a timestamp.
		fmt.Fprintf(os.Stderr, "%v error: %v\n", time.Now().Format(time.RFC3339), err)
		os.Exit(1)
	}
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
