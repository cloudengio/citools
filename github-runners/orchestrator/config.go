package main

import (
	"context"
	"fmt"
	"time"

	"cloudeng.io/cmdutil"
	"cloudeng.io/file/crawl/crawlcmd"
	macoskeychain "cloudeng.io/macos/keychain/plugin"
	"cloudeng.io/webapi/operations"
	"cloudeng.io/webapp/webassets"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubclient"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
)

type Config struct {
	Logging        cmdutil.LoggingConfig           `yaml:"logging" doc:"logging configuration options"`
	Global         GlobalConfig                    `yaml:"global" doc:"global configuration options"`
	Repositories   []githubclient.RepositoryConfig `yaml:"repositories" doc:"list of GitHub repositories to manage runners for"`
	ICloudKeychain ICloudKeychainConfig            `yaml:"icloud_keychain" doc:"icloud keychain configuration for loading API keys"`
	VMPools        map[string]vmsclient.PoolConfig `yaml:"vm_pools" doc:"configuration for VM pools to use for runner provisioning"`
	Webhook        WebhookConfig                   `yaml:"webhook" doc:"configuration for the GitHub webhook relay service"`
	WebUI          WebUIConfig                     `yaml:"web_ui" doc:"configuration for the management web UI and JSON API"`
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
