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
	GitHubToken        string        `yaml:"github_token"`
	Repositories       []Repository  `yaml:"repositories"`
	BaseVMName         string        `yaml:"base_vm_name"`
	PollInterval       time.Duration `yaml:"poll_interval"`
	MaxConcurrentVMs   int           `yaml:"max_concurrent_vms"`
	KeepFailedDuration time.Duration `yaml:"keep_failed_duration"`
}

type VMState string

const (
	StateProvisioning VMState = "provisioning"
	StateIdle         VMState = "idle"
	StateAssigned     VMState = "assigned"
	StateFailed       VMState = "failed"
	StateStopping     VMState = "stopping"
)

type ManagedVM struct {
	Name       string
	Owner      string
	Repo       string
	CancelFunc context.CancelFunc
	State      VMState
	IP         string
	AssignedAt time.Time
}

var verbose bool
var keepFailedDur time.Duration

func main() {
	configPath := flag.String("config", "config.yaml", "Path to YAML configuration file")
	flag.BoolVar(&verbose, "v", false, "Enable verbose logging")
	flag.DurationVar(&keepFailedDur, "keep-failed", 0, "Duration to keep failed VMs alive (e.g. 30m, 1h)")
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

	// Flag overrides config
	if keepFailedDur > 0 {
		cfg.KeepFailedDuration = keepFailedDur
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

	log.Printf("Starting orchestrator. Max Concurrent VMs (Pool Size): %d", cfg.MaxConcurrentVMs)

	for {
		mu.Lock()
		currentRunningCount := len(activeVMs)
		
		// 1. Maintain Pool Size
		if currentRunningCount < cfg.MaxConcurrentVMs {
			countToStart := cfg.MaxConcurrentVMs - currentRunningCount
			for i := 0; i < countToStart; i++ {
				vmName := fmt.Sprintf("%s-%d", cfg.BaseVMName, time.Now().UnixNano()/1e6)
				log.Printf("[%s] Starting new pooled VM", vmName)
				vmCtx, vmCancel := context.WithCancel(ctx)
				vm := &ManagedVM{
					Name:       vmName,
					State:      StateProvisioning,
					CancelFunc: vmCancel,
				}
				activeVMs[vmName] = vm

				wg.Add(1)
				go func(name string, vctx context.Context, vcancel context.CancelFunc) {
					defer wg.Done()
					defer vcancel()
					
					// Provisioning phase
					if err := tart.Clone(vctx, name); err != nil {
						log.Printf("[%s] Clone failed: %v", name, err)
						mu.Lock()
						delete(activeVMs, name)
						mu.Unlock()
						return
					}

					if _, err := tart.Run(vctx, name); err != nil {
						log.Printf("[%s] Run failed: %v", name, err)
						_ = tart.Delete(context.Background(), name)
						mu.Lock()
						delete(activeVMs, name)
						mu.Unlock()
						return
					}

					ip, err := tart.GetIP(vctx, name)
					if err != nil {
						log.Printf("[%s] Failed to get IP: %v", name, err)
						mu.Lock()
						delete(activeVMs, name)
						mu.Unlock()
						_ = tart.Delete(context.Background(), name)
						return
					}

					mu.Lock()
					if v, ok := activeVMs[name]; ok {
						v.IP = ip
						v.State = StateIdle
						log.Printf("[%s] VM is now IDLE and ready for assignment (IP: %s)", name, ip)
					}
					mu.Unlock()

					// Keep running until context is cancelled (either assigned job finishes or orchestrator stops)
					<-vctx.Done()
					log.Printf("[%s] VM context done, cleaning up...", name)
					
					// Cleanup
					mu.Lock()
					delete(activeVMs, name)
					mu.Unlock()
					_ = tart.Delete(context.Background(), name)
				}(vmName, vmCtx, vmCancel)
				
				time.Sleep(100 * time.Millisecond)
			}
		}
		mu.Unlock()

		// 2. Check for Queued Jobs and Assign Idle VMs
		for _, repo := range cfg.Repositories {
			gh := &GitHub{Token: cfg.GitHubToken, Owner: repo.Owner, Repo: repo.Repo, Verbose: verbose}
			queued, err := gh.GetQueuedRunCount(ctx)
			if err != nil {
				log.Printf("[%s/%s] Error getting queued count: %v", repo.Owner, repo.Repo, err)
				continue
			}

			if queued > 0 {
				runners, err := gh.GetRunners(ctx)
				if err != nil {
					log.Printf("[%s/%s] Error getting runners: %v", repo.Owner, repo.Repo, err)
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
							log.Printf("[%s/%s] Found %d queued jobs. Assigning VM %s", repo.Owner, repo.Repo, queued, idleVM.Name)
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
										log.Printf("[%s] Failed to create registration token for %s/%s: %v", vm.Name, vm.Owner, vm.Repo, err)
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
									log.Printf("[%s] Failed to inject config: %v", vm.Name, err)
									vm.CancelFunc()
									return
								}
								log.Printf("[%s] Successfully assigned to %s/%s. Runner is starting.", vm.Name, vm.Owner, vm.Repo)
								
								go monitorJob(ctx, vm, g, cfg.KeepFailedDuration)
							}(idleVM, gh, repo)
						} else {
							mu.Unlock()
							if verbose {
								log.Printf("[%s/%s] Needs VM for queued job, but pool is empty/provisioning.", repo.Owner, repo.Repo)
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
		
		log.Printf("Pool Status: %d Provisioning, %d Idle, %d Assigned, %d Failed (kept alive). Total: %d/%d", 
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
				log.Printf("[%s] Job finished with conclusion: %s", vm.Name, conclusion)
				if conclusion == "success" {
					log.Printf("[%s] Success! Releasing VM for cleanup.", vm.Name)
				} else {
					log.Printf("[%s] Job failed (%s).", vm.Name, conclusion)
					if keepFailedDuration > 0 {
						log.Printf("[%s] Keeping VM alive for %v for debugging.", vm.Name, keepFailedDuration)
						vm.State = StateFailed
						select {
						case <-time.After(keepFailedDuration):
						case <-ctx.Done():
						}
					}
					log.Printf("[%s] Releasing VM for cleanup (will be deleted).", vm.Name)
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
						log.Printf("[%s] Runner went offline. Cleaning up.", vm.Name)
						vm.CancelFunc()
						return
					}
					break
				}
			}
			// If we assigned it but it never showed up or was deleted from GH
			if !found && time.Since(vm.AssignedAt) > 5*time.Minute {
				log.Printf("[%s] Runner not found in GitHub after 5m. Cleaning up.", vm.Name)
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

