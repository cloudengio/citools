package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

type Repository struct {
	Owner      string `yaml:"owner"`
	Repo       string `yaml:"repo"`
	TargetIdle int    `yaml:"target_idle"`
}

type Config struct {
	GitHubToken      string        `yaml:"github_token"`
	Repositories     []Repository  `yaml:"repositories"`
	BaseVMName       string        `yaml:"base_vm_name"`
	PollInterval     time.Duration `yaml:"poll_interval"`
	MaxConcurrentVMs int           `yaml:"max_concurrent_vms"`
}

type ManagedVM struct {
	Name       string
	Owner      string
	Repo       string
	CancelFunc context.CancelFunc
	Busy       bool
}

var verbose bool

func main() {
	configPath := flag.String("config", "config.yaml", "Path to YAML configuration file")
	flag.BoolVar(&verbose, "v", false, "Enable verbose logging")
	flag.Parse()

	if verbose {
		log.Println("Verbose logging enabled")
	}

	data, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("Error parsing config file: %v", err)
	}

	if cfg.BaseVMName == "" {
		cfg.BaseVMName = "macos-ci"
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 15 * time.Second
	}
	if cfg.MaxConcurrentVMs == 0 {
		cfg.MaxConcurrentVMs = 2
	}

	if cfg.GitHubToken == "" {
		log.Println("github_token not set in config, attempting to use GitHub CLI (gh auth token)...")
		out, err := exec.Command("gh", "auth", "token").Output()
		if err != nil {
			log.Fatal("github_token is not set and 'gh auth token' failed. Please login with 'gh auth login'.")
		}
		cfg.GitHubToken = string(strings.TrimSpace(string(out)))
	}

	if len(cfg.Repositories) == 0 {
		log.Fatal("at least one repository must be defined in config.yaml")
	}

	tart := &Tart{BaseName: cfg.BaseVMName, Verbose: verbose}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	activeVMs := make(map[string]*ManagedVM)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	log.Printf("Starting orchestrator for %d repos. Max Concurrent VMs: %d", len(cfg.Repositories), cfg.MaxConcurrentVMs)

	for {
		// 1. Collect needs across all repos
		for _, repo := range cfg.Repositories {
			// If not set, default to 1 so we always have a warm VM
			if repo.TargetIdle == 0 {
				repo.TargetIdle = 1
			}

			gh := &GitHub{Token: cfg.GitHubToken, Owner: repo.Owner, Repo: repo.Repo, Verbose: verbose}

			queued, err := gh.GetQueuedRunCount(ctx)
			if err != nil {
				log.Printf("[%s/%s] Error getting queued count: %v", repo.Owner, repo.Repo, err)
				continue
			}
			runners, err := gh.GetRunners(ctx)
			if err != nil {
				log.Printf("[%s/%s] Error getting runners: %v", repo.Owner, repo.Repo, err)
				continue
			}

			idleCount := 0
			busyCount := 0
			for _, r := range runners {
				if strings.HasPrefix(r.Name, cfg.BaseVMName) && r.Status == "online" {
					if r.Busy {
						busyCount++
					} else {
						idleCount++
					}
					// Update local tracking
					mu.Lock()
					if vm, ok := activeVMs[r.Name]; ok {
						vm.Busy = r.Busy
					}
					mu.Unlock()
				}
			}

			if verbose {
				log.Printf("[%s/%s] Queued: %d, Idle: %d, Busy: %d, TargetIdle: %d", repo.Owner, repo.Repo, queued, idleCount, busyCount, repo.TargetIdle)
			}

			// Decision logic:
			// If we have queued jobs and no idle runners, we need a VM URGENTLY.
			// Else if we have less than target_idle runners, we need a VM.
			
			needsVM := false
			urgent := false
			reason := ""
			if queued > idleCount {
				needsVM = true
				urgent = true
				reason = fmt.Sprintf("queued jobs (%d) > idle runners (%d)", queued, idleCount)
			} else if idleCount < repo.TargetIdle {
				needsVM = true
				reason = fmt.Sprintf("idle runners (%d) < target idle (%d)", idleCount, repo.TargetIdle)
			}

			if needsVM {
				mu.Lock()
				currentVMCount := len(activeVMs)
				canStart := currentVMCount < cfg.MaxConcurrentVMs
				mu.Unlock()

				if verbose {
					log.Printf("[%s/%s] Needs VM: %s. canStart: %v (active: %d, max: %d)", repo.Owner, repo.Repo, reason, canStart, currentVMCount, cfg.MaxConcurrentVMs)
				}

				if !canStart && urgent {
					// Reallocation logic: can we kill an idle VM from ANOTHER repo?
					mu.Lock()
					var victim *ManagedVM
					for name, vm := range activeVMs {
						if !vm.Busy && (vm.Owner != repo.Owner || vm.Repo != repo.Repo) {
							victim = vm
							delete(activeVMs, name)
							break
						}
					}
					mu.Unlock()

					if victim != nil {
						log.Printf("[%s/%s] Urgent need. Stopping idle VM %s from %s/%s to free slot.", repo.Owner, repo.Repo, victim.Name, victim.Owner, victim.Repo)
						victim.CancelFunc() // Stop the VM
						// Wait a moment for slot to actually clear
						time.Sleep(2 * time.Second)
						canStart = true
					}
				}

				if canStart {
					vmName := fmt.Sprintf("%s-%s-%d", cfg.BaseVMName, strings.ToLower(repo.Repo), time.Now().Unix())
					log.Printf("[%s/%s] Decided to start new VM: %s", repo.Owner, repo.Repo, vmName)
					vmCtx, vmCancel := context.WithCancel(ctx)
					
					mu.Lock()
					activeVMs[vmName] = &ManagedVM{
						Name:       vmName,
						Owner:      repo.Owner,
						Repo:       repo.Repo,
						CancelFunc: vmCancel,
					}
					mu.Unlock()

					wg.Add(1)
					go func(r Repository, g *GitHub, name string, vctx context.Context, vcancel context.CancelFunc) {
						defer wg.Done()
						defer vcancel()
						defer func() {
							mu.Lock()
							delete(activeVMs, name)
							log.Printf("[%s] Removed from tracking. Active VMs: %d", name, len(activeVMs))
							mu.Unlock()
						}()

						if err := provisionAndRunVM(vctx, tart, g, name); err != nil {
							log.Printf("[%s] VM Failed: %v", name, err)
						}
					}(repo, gh, vmName, vmCtx, vmCancel)
				}
			}
		}

		mu.Lock()
		currentRunningCount := len(activeVMs)
		mu.Unlock()
		log.Printf("Status: %d/%d VMs running", currentRunningCount, cfg.MaxConcurrentVMs)

		select {
		case <-ticker.C:
		case <-ctx.Done():
			wg.Wait()
			return
		}
	}
}

func provisionAndRunVM(ctx context.Context, tart *Tart, gh *GitHub, vmName string) error {
	log.Printf("[%s] Provisioning...", vmName)
	if err := tart.Clone(ctx, vmName); err != nil {
		if verbose {
			log.Printf("[%s] Clone failed: %v", vmName, err)
		}
		return err
	}

	cmd, err := tart.Run(ctx, vmName)
	if err != nil {
		if verbose {
			log.Printf("[%s] Run failed: %v", vmName, err)
		}
		_ = tart.Delete(context.Background(), vmName)
		return err
	}

	// Lifecycle in background
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	// Pre-stage
	ip, err := tart.GetIP(ctx, vmName)
	if err != nil {
		if verbose {
			log.Printf("[%s] Failed to get IP: %v", vmName, err)
		}
		return fmt.Errorf("failed to get IP: %w", err)
	}

	token, err := gh.CreateRegistrationToken(ctx)
	if err != nil {
		if verbose {
			log.Printf("[%s] Failed to create registration token: %v", vmName, err)
		}
		return fmt.Errorf("failed to create registration token: %w", err)
	}

	url := fmt.Sprintf("https://github.com/%s/%s", gh.Owner, gh.Repo)
	if err := tart.InjectConfig(ctx, ip, url, token, vmName, "macos-sequoia,arm64"); err != nil {
		if verbose {
			log.Printf("[%s] Failed to inject config: %v", vmName, err)
		}
		return fmt.Errorf("failed to inject config: %w", err)
	}
	log.Printf("[%s] Ready for %s/%s", vmName, gh.Owner, gh.Repo)

	select {
	case <-ctx.Done():
		// Forced stop (e.g. reallocation or shutdown)
		log.Printf("[%s] Stopping VM (context cancelled)", vmName)
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		<-done
	case err := <-done:
		log.Printf("[%s] Finished (exited: %v)", vmName, err)
	}

	// Cleanup logic
	time.Sleep(10 * time.Second)
	conclusion, _ := gh.GetRunnerJobConclusion(context.Background(), vmName)
	if conclusion == "success" || conclusion == "" {
		_ = tart.Delete(context.Background(), vmName)
	} else {
		log.Printf("[%s] Job failed (%s). RETAINING VM.", vmName, conclusion)
	}
	return nil
}
