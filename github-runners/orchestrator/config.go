package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloudeng.io/cmdutil"
	"cloudeng.io/cmdutil/cmdyaml"
	"cloudeng.io/file/crawl/crawlcmd"
	macoskeychain "cloudeng.io/macos/keychain/plugin"
	"cloudeng.io/webapi/operations"
	"cloudeng.io/webapp/webassets"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubclient"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
	"gopkg.in/natefinch/lumberjack.v2"
)

type LoggingConfig struct {
	cmdutil.LoggingConfig `yaml:",inline"`
	MaxSize               cmdyaml.ByteSize `yaml:"max_size" doc:"maximum size of a log file before it is rotated; defaults to 10MB"`
	MaxBackups            int              `yaml:"max_backups" doc:"maximum number of rotated log files to retain; defaults to 5"`
}

func (lc LoggingConfig) Options() []cmdutil.LoggingOption {
	if lc.File == "" || lc.File == "-" {
		return nil
	}
	if lc.MaxSize == 0 {
		lc.MaxSize = 10 * cmdyaml.MB
	}
	if lc.MaxBackups == 0 {
		lc.MaxBackups = 5
	}
	logger := lumberjack.Logger{
		Filename:   lc.File,
		MaxSize:    int(lc.MaxSize / cmdyaml.MB),
		MaxBackups: lc.MaxBackups,
		LocalTime:  true,
	}
	return []cmdutil.LoggingOption{cmdutil.WithWriteCloser(&logger)}
}

type Config struct {
	Logging        LoggingConfig                   `yaml:"logging" doc:"logging configuration"`
	Global         GlobalConfig                    `yaml:"global" doc:"global configuration options"`
	Repositories   []githubclient.RepositoryConfig `yaml:"repositories" doc:"list of GitHub repositories to manage runners for"`
	ICloudKeychain ICloudKeychainConfig            `yaml:"icloud_keychain" doc:"icloud keychain configuration for loading API keys"`
	VMPools        map[string]vmsclient.PoolConfig `yaml:"vm_pools" doc:"configuration for VM pools to use for runner provisioning"`
	Webhook        WebhookConfig                   `yaml:"webhook" doc:"configuration for the GitHub webhook relay service"`
	WebUI          WebUIConfig                     `yaml:"web_ui" doc:"configuration for the management web UI and JSON API"`
}

// LaunchAgentConfig configures the launchd job that the service install command
// writes to run the orchestrator at login. It is read from its own file, which
// ships in the app bundle's Resources directory, rather than from the
// orchestrator configuration, so that the service can be installed before any
// orchestrator configuration exists. Every field is optional; the accessors
// below supply the defaults for those left unset.
type LaunchAgentConfig struct {
	RunAtLoad            *bool             `yaml:"run_at_load" doc:"start the orchestrator as soon as the service is loaded, ie. at login. Defaults to true."`
	KeepAlive            *bool             `yaml:"keep_alive" doc:"have launchd restart the orchestrator whenever it exits. Defaults to true."`
	EnvironmentVariables map[string]string `yaml:"environment_variables" doc:"environment variables for the orchestrator. Defaults to a PATH that includes the Homebrew locations, since launchd provides only a minimal one and the orchestrator invokes tart, go and docker."`
	LogDir               string            `yaml:"log_dir" doc:"directory for the service's stdout and stderr log files. Defaults to ~/Library/Logs."`
	RunArgs              []string          `yaml:"run_args" doc:"arguments the service runs the orchestrator with. Defaults to run --delete-orphaned-vms."`
}

// DefaultServicePath is the PATH given to the orchestrator when it is run by
// launchd, which otherwise provides too minimal a one to find Homebrew tools.
const DefaultServicePath = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// RunAtLoadOrDefault reports whether the service should start at login.
func (lc LaunchAgentConfig) RunAtLoadOrDefault() bool {
	return lc.RunAtLoad == nil || *lc.RunAtLoad
}

// KeepAliveOrDefault reports whether launchd should restart the orchestrator
// when it exits.
func (lc LaunchAgentConfig) KeepAliveOrDefault() bool {
	return lc.KeepAlive == nil || *lc.KeepAlive
}

// EnvironmentOrDefault returns the environment for the service.
func (lc LaunchAgentConfig) EnvironmentOrDefault() map[string]string {
	if len(lc.EnvironmentVariables) > 0 {
		return lc.EnvironmentVariables
	}
	return map[string]string{"PATH": DefaultServicePath}
}

// LogDirOrDefault returns the directory the service's log files are written to,
// expanding a leading ~. Neither the config parser nor launchd expands it, and a
// hand-edited config will naturally use one.
func (lc LaunchAgentConfig) LogDirOrDefault() string {
	home, _ := os.UserHomeDir()
	switch {
	case lc.LogDir == "":
		return filepath.Join(home, "Library", "Logs")
	case lc.LogDir == "~":
		return home
	case strings.HasPrefix(lc.LogDir, "~/"):
		return filepath.Join(home, lc.LogDir[2:])
	}
	return lc.LogDir
}

// RunArgsOrDefault returns the arguments the service runs the orchestrator with.
func (lc LaunchAgentConfig) RunArgsOrDefault() []string {
	if len(lc.RunArgs) > 0 {
		return lc.RunArgs
	}
	return []string{"run", "--delete-orphaned-vms"}
}

// WebUIConfig configures the management web UI and its OpenAPI-specified JSON
// API, served by the run command.
type WebUIConfig struct {
	Enabled       bool             `yaml:"enabled" doc:"enable the web UI and management API"`
	ListenAddress string           `yaml:"listen_address" doc:"address for the web UI/API HTTP server, e.g. 127.0.0.1:8088"`
	Reload        webassets.Config `yaml:"reload" doc:"optionally serve the web UI assets from the local filesystem (reload_root should point at the webui directory) so the SPA can be rebuilt without recompiling the binary"`
}

type GlobalConfig struct {
	TmpDir                      string        `yaml:"tmp_dir" doc:"directory in which to create temporary files and directories"`
	SearchPaths                 []string      `yaml:"search_paths" doc:"list of paths to search for required tools (e.g. tart etc.)"`
	CompletionQueueSize         int           `yaml:"completion_queue_size" doc:"maximum number of completion events to retain in memory"`
	FailedVMRetentionPeriod     time.Duration `yaml:"failed_vm_retention_period" doc:"duration for which failed VMs should be retained before being deleted"`
	SuccessfulVMRetentionPeriod time.Duration `yaml:"successful_vm_retention_period" doc:"duration for which successful VMs should be retained before being deleted"`
}

func (cfg Config) Validate() error {
	for _, repo := range cfg.Repositories {
		if err := repo.Validate(); err != nil {
			return err
		}
	}
	if err := cfg.validateVMPoolNames(); err != nil {
		return err
	}
	for k, pool := range cfg.VMPools {
		if err := pool.Validate(); err != nil {
			return fmt.Errorf("vm_pool %s: %w", k, err)
		}
	}
	return nil
}

func (cfg Config) validateVMPoolNames() error {
	for _, repo := range cfg.Repositories {
		for _, runner := range repo.Runners {
			if _, ok := cfg.VMPools[runner.VMPoolName]; !ok {
				return fmt.Errorf("repository %s: runner %s: vm_pool %q not defined in vm_pools",
					repo.Service.Repo, runner.NamePrefix, runner.VMPoolName)
			}
		}
	}
	return nil
}

type ICloudKeychainConfig struct {
	macoskeychain.Config `yaml:",inline"`
	Items                []string `yaml:"items" doc:"list of keychain item names to use for API keys (the value of the keychain items should be in cloudeng.io/cmdutil/keys.Info format)"`
}

type contextConfigKey struct{}

func ContextWithConfig(ctx context.Context, cfg Config) context.Context {
	return context.WithValue(ctx, contextConfigKey{}, cfg)
}

func ConfigFromContext(ctx context.Context) (Config, bool) {
	cfg, ok := ctx.Value(contextConfigKey{}).(Config)
	return cfg, ok
}

type WebhookConfig struct {
	RelayURL    string               `yaml:"relay_url" doc:"the URL to which GitHub will send webhook events"`
	RateControl crawlcmd.RateControl `yaml:"rate_control" doc:"rate control settings for the webhook relay service"`
}

func (wc WebhookConfig) Options() ([]operations.Option, error) {
	opts := []operations.Option{}
	rc, err := wc.RateControl.NewRateController()
	if err != nil {
		return nil, err
	}
	opts = append(opts, operations.WithRateController(rc, wc.RateControl.ExponentialBackoff.StatusCodes...))
	return opts, nil
}
