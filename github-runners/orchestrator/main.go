// Copyright 2026 cloudeng llc. All rights reserved."
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/cmdyaml"
	"cloudeng.io/cmdutil/keys"
	"cloudeng.io/cmdutil/subcmd"
	"cloudeng.io/logging/ctxlog"
)

const CmdSpecYAML = `
name: github-runner-orchestrator
summary: orchestrator for GitHub self-hosted runners
commands:
  - name: run
    summary: run the orchestrator with the specified configuration
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
  - name: config
    summary: config related commands
    commands:
      - name: show
        summary: show the current configuration
      - name: describe
        summary: describe the configuration spec
`

type GlobalFlags struct {
	ConfigFile string `subcmd:"config,github_orchestrator_config.yml,path to YAML configuration file" doc:"path to YAML configuration file"`
	cmdutil.LoggingFlags
	Verbose bool `subcmd:"verbose,false,enable verbose logging" doc:"enable verbose logging"`
}

func createCLI() *subcmd.CommandSetYAML {
	cmdSet := subcmd.MustFromYAML(CmdSpecYAML)

	runCmd := RunCommand{}
	cmdSet.Set("run").MustRunner(runCmd.Run, &struct{}{})

	cfgCmd := ConfigCommand{}
	cmdSet.Set("config", "show").MustRunner(cfgCmd.Show, &struct{}{})
	cmdSet.Set("config", "describe").MustRunner(cfgCmd.Describe, &struct{}{})

	ghCmd := GitHubCommand{}
	cmdSet.Set("github", "list-runners").MustRunner(ghCmd.ListRunners, &ListRunnersFlags{})
	cmdSet.Set("github", "list-runs").MustRunner(ghCmd.ListRuns, &ListRunsFlags{})
	cmdSet.Set("github", "get-runs").MustRunner(ghCmd.GetRuns, &GetRunsFlags{})
	//cmdSet.Set("github", "queued-runs").MustRunner(ghCmd.QueuedRuns, &QueuedRunsFlags{})
	cmdSet.Set("github", "create-webhook").MustRunner(ghCmd.CreateWebhook, &CreateWebhookFlags{})
	cmdSet.Set("github", "create-registration-token").MustRunner(ghCmd.CreateRegistrationToken, &CreateRegistrationTokenFlags{})
	cmdSet.Set("github", "runner-job-conclusion").MustRunner(ghCmd.GetJobConclusion, &GetJobConclusionFlags{})

	return cmdSet
}

var globalFlags GlobalFlags

func verbose() bool {
	return globalFlags.Verbose
}

func main() {
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
		var cfg Config
		if err := cmdyaml.ParseConfigFiles(ctx, &cfg, globalFlags.ConfigFile); err != nil {
			return fmt.Errorf("Error reading config file: %v", err)
		}
		if cfg.Validate() != nil {
			return fmt.Errorf("invalid config: %v", cfg.Validate())
		}
		loggerConfig := cfg.Logging.WithFlagOverrides(
			fs.FlagSet(), globalFlags.LoggingFlags)
		opts := loggerConfig.Options()
		logger, err := loggerConfig.NewLoggerOpts(opts)
		if err != nil {
			return fmt.Errorf("Error setting up logger: %v", err)
		}
		ctx = ctxlog.WithLogger(ctx, logger.Logger)
		ctx = ContextWithConfig(ctx, cfg)
		logCloser = logger.Close
		if len(cfg.ICloudKeychain.Items) > 0 {
			ctx, err = loadKeychain(ctx, cfg.ICloudKeychain)
			if err != nil {
				return fmt.Errorf("Error loading keychain: %v", err)
			}
		}
		return cmdRunner(ctx)
	})
	cmdutil.HandleSignals(func() { cancel(cmdutil.ErrInterrupt) }, os.Interrupt)
	if err := cli.Dispatch(ctx); err != nil {
		if errors.Is(err, cmdutil.ErrInterrupt) {
			return
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
}

func loadKeychain(ctx context.Context, cfg ICloudKeychainConfig) (context.Context, error) {
	ims, ok := keys.KeyStoreFromContext(ctx)
	if !ok {
		return ctx, fmt.Errorf("no keystore in context")
	}
	fs := cfg.FS()
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

/*
func xxx() {
	configPath := flag.String("config", "config.yaml", "Path to YAML configuration file")
	flag.BoolVar(&verbose, "v", false, "Enable verbose logging")
	flag.DurationVar(&keepFailedDur, "keep-failed", 0, "Duration to keep failed VMs alive (e.g. 30m, 1h)")
	flag.Parse()

	if verbose {
		ctxlog.Info(ctx, "Verbose logging enabled")
	}

	cfg, err := ReadConfigFile(*configPath)
	if err != nil {
		ctxlog.Info(ctx, "Error reading config: %v", err)
	}

	if cfg.GitHubToken == "" {
		ctxlog.Info(ctx, "github_token not set in config, attempting to use GitHub CLI (gh auth token)...")
		out, err := exec.Command("gh", "auth", "token").Output()
		if err != nil {
			ctxlog.Info(ctx, "github_token is not set and 'gh auth token' failed. Please login with 'gh auth login'.")
		}
		cfg.GitHubToken = string(strings.TrimSpace(string(out)))
	}

	if len(cfg.Repositories) == 0 {
		ctxlog.Info(ctx, "at least one repository must be defined in config.yaml")
	}

	tart := &Tart{BaseName: cfg.BaseVMName, Verbose: verbose}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	activeVMs := make(map[string]*ManagedVM)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	ctxlog.Info(ctx, "Starting orchestrator. Max Concurrent VMs (Pool Size): %d", cfg.MaxConcurrentVMs)

	for {
		mu.Lock()
		currentRunningCount := len(activeVMs)



		// 2. Check for Queued Jobs and Assign Idle VMs
		for _, repo := range cfg.Repositories {
			gh := &GitHub{Token: cfg.GitHubToken, Owner: repo.Owner, Repo: repo.Repo, Verbose: verbose}
			queued, err := gh.GetQueuedRunCount(ctx)
			if err != nil {
				ctxlog.Info(ctx, "[%s/%s] Error getting queued count: %v", repo.Owner, repo.Repo, err)
				continue
			}

			if queued > 0 {
				runners, err := gh.GetRunners(ctx)
				if err != nil {
					ctxlog.Info(ctx, "[%s/%s] Error getting runners: %v", repo.Owner, repo.Repo, err)
					continue
				}

				activeRunners := 0
				for _, r := range runners {
					if r.Status == "online" {
						activeRunners++
					}
				}

				if queued > activeRunners {
					needed := queued - activeRunners
					for i := 0; i < needed; i++ {
						mu.Lock()
						var idleVM *ManagedVM
						for _, v := range activeVMs {
							if v.State == StateIdle {
								idleVM = v
								break
							}
						}

						if idleVM != nil {
							ctxlog.Info(ctx, "[%s/%s] Found %d queued jobs. Assigning VM %s", repo.Owner, repo.Repo, queued, idleVM.Name)
							idleVM.State = StateAssigned
							idleVM.Owner = repo.Owner
							idleVM.Repo = repo.Repo
							idleVM.AssignedAt = time.Now()
							mu.Unlock()

							// Start assignment in background
							go func(vm *ManagedVM, g *GitHub, r Repository) {
								token := r.Token
								if token == "" {
									var err error
									token, err = g.CreateRegistrationToken(ctx)
									if err != nil {
										ctxlog.Info(ctx, "[%s] Failed to create registration token for %s/%s: %v", vm.Name, vm.Owner, vm.Repo, err)
										vm.CancelFunc()
										return
									}
								}

								url := r.URL
								if url == "" {
									url = fmt.Sprintf("https://github.com/%s/%s", g.Owner, g.Repo)
								}

								labels := r.Labels
								if labels == "" {
									labels = "macos,arm64,macos-sequoia"
								}

								if err := tart.InjectConfig(ctx, vm.IP, url, token, vm.Name, labels); err != nil {
									ctxlog.Info(ctx, "[%s] Failed to inject config: %v", vm.Name, err)
									vm.CancelFunc()
									return
								}
								ctxlog.Info(ctx, "[%s] Successfully assigned to %s/%s. Runner is starting.", vm.Name, vm.Owner, vm.Repo)

								go monitorJob(ctx, vm, g, cfg.KeepFailedDuration)
							}(idleVM, gh, repo)
						} else {
							mu.Unlock()
							if verbose {
								ctxlog.Info(ctx, "[%s/%s] Needs VM for queued job, but pool is empty/provisioning.", repo.Owner, repo.Repo)
							}
							break
						}
					}
				}
			}
		}

		mu.Lock()
		statusProvisioning := 0
		statusIdle := 0
		statusAssigned := 0
		statusFailed := 0
		for _, v := range activeVMs {
			switch v.State {
			case StateProvisioning:
				statusProvisioning++
			case StateIdle:
				statusIdle++
			case StateAssigned:
				statusAssigned++
			case StateFailed:
				statusFailed++
			}
		}
		total := len(activeVMs)
		mu.Unlock()

		ctxlog.Info(ctx, "Pool Status: %d Provisioning, %d Idle, %d Assigned, %d Failed (kept alive). Total: %d/%d",
			statusProvisioning, statusIdle, statusAssigned, statusFailed, total, cfg.MaxConcurrentVMs)

		select {
		case <-ticker.C:
		case <-ctx.Done():
			wg.Wait()
			return
		}
	}
}

func monitorJob(ctx context.Context, vm *ManagedVM, gh *GitHub, keepFailedDuration time.Duration) {
	// Wait a bit for the runner to actually start and pick up the job
	time.Sleep(30 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Check if job finished
		conclusion, err := gh.GetRunnerJobConclusion(ctx, vm.Name)
		if err == nil {
			if conclusion != "" {
				ctxlog.Info(ctx, "[%s] Job finished with conclusion: %s", vm.Name, conclusion)
				if conclusion == "success" {
					ctxlog.Info(ctx, "[%s] Success! Releasing VM for cleanup.", vm.Name)
				} else {
					ctxlog.Info(ctx, "[%s] Job failed (%s).", vm.Name, conclusion)
					if keepFailedDuration > 0 {
						ctxlog.Info(ctx, "[%s] Keeping VM alive for %v for debugging.", vm.Name, keepFailedDuration)
						vm.State = StateFailed
						select {
						case <-time.After(keepFailedDuration):
						case <-ctx.Done():
						}
					}
					ctxlog.Info(ctx, "[%s] Releasing VM for cleanup (will be deleted).", vm.Name)
				}
				vm.CancelFunc()
				return
			}
		}

		// Also check if the runner is still online
		runners, err := gh.GetRunners(ctx)
		if err == nil {
			found := false
			for _, r := range runners {
				if r.Name == vm.Name {
					found = true
					if r.Status == "offline" {
						ctxlog.Info(ctx, "[%s] Runner went offline. Cleaning up.", vm.Name)
						vm.CancelFunc()
						return
					}
					break
				}
			}
			// If we assigned it but it never showed up or was deleted from GH
			if !found && time.Since(vm.AssignedAt) > 5*time.Minute {
				ctxlog.Info(ctx, "[%s] Runner not found in GitHub after 5m. Cleaning up.", vm.Name)
				vm.CancelFunc()
				return
			}
		}

		select {
		case <-time.After(20 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}
*/
