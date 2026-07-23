package main

import (
	"context"
	"fmt"
	"strings"

	"cloudeng.io/cmdutil"
	macoskeychain "cloudeng.io/macos/keychain/plugin"
	"cloudeng.io/webapi/clients/github/githubcmd"
	"cloudeng.io/webapi/operations/apicrawlcmd"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Logging        cmdutil.LoggingConfig    `yaml:"logging" doc:"logging configuration options"`
	Repositories   []RepositoryConfig       `yaml:"repositories" doc:"list of GitHub repositories to manage runners for"`
	ICloudKeychain ICloudKeychainConfig     `yaml:"icloud_keychain" doc:"icloud keychain configuration for loading API keys"`
	VMPools        []VMPoolConfig           `yaml:"vm_pools" doc:"configuration for VM pools to use for runner provisioning"`
	Webhooks       map[string]WebhookConfig `yaml:"webhooks" doc:"configuration for GitHub webhooks"`

	// KeepFailedDuration time.Duration `yaml:"keep_failed_duration"`
}

func (cfg Config) Validate() error {
	for _, repo := range cfg.Repositories {
		if err := repo.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type ICloudKeychainConfig struct {
	macoskeychain.Config `yaml:",inline"`
	Items                []string `yaml:"items" doc:"list of keychain item names to use for API keys (the value of the keychain items should be in cloudeng.io/cmdutil/keys.Info format)"`
}

type RepositoryConfig struct {
	apicrawlcmd.Crawl[githubcmd.Service] `yaml:",inline"`
	Runners                              []GitHubRunnerConfig `yaml:"runners" doc:"list of runner configurations for this repository"`
}

// UnmarshalYAML implements yaml.Unmarshaler. The embedded
// apicrawlcmd.Crawl[githubcmd.Service] expects the GitHub service fields to be
// nested under a "service_config" key and the API key under "key_id", but the
// orchestrator config lists them flat within each repository entry (owner,
// organization, repo, per_page, api_key_id). This method accepts the flat
// layout while still honoring the nested Crawl form when present.
func (rc *RepositoryConfig) UnmarshalYAML(node *yaml.Node) error {
	var aux struct {
		apicrawlcmd.Crawl[githubcmd.Service] `yaml:",inline"`
		Owner                                string               `yaml:"owner"`
		Organization                         string               `yaml:"organization"`
		Repo                                 string               `yaml:"repo"`
		PerPage                              int                  `yaml:"per_page"`
		APIKeyID                             string               `yaml:"api_key_id"`
		Runners                              []GitHubRunnerConfig `yaml:"runners"`
	}
	if err := node.Decode(&aux); err != nil {
		return err
	}
	rc.Crawl = aux.Crawl
	rc.Runners = aux.Runners
	// Flat fields take precedence over the nested service_config form when set.
	if aux.Owner != "" {
		rc.Service.Owner = aux.Owner
	}
	if aux.Repo != "" {
		rc.Service.Repo = aux.Repo
	}
	if aux.PerPage != 0 {
		rc.Service.PerPage = aux.PerPage
	} else {
		rc.Service.PerPage = githubcmd.DefaultPageSize
	}
	if aux.APIKeyID != "" {
		rc.KeyID = aux.APIKeyID
	}
	if aux.UserID != "" {
		rc.UserID = aux.UserID
	}
	return nil
}

func (rc RepositoryConfig) Validate() error {
	if err := rc.Service.Validate(); err != nil {
		return err
	}
	if len(rc.Runners) == 0 {
		return fmt.Errorf("repository %s: at least one runner configuration is required", rc.Service.Repo)
	}
	return nil
}

type GitHubRunnerConfig struct {
	// RunnerVM    string   `yaml:"runner_vm"`
	RunnderDir    string   `yaml:"runner_dir" doc:"directory on the VM to install the runner"`
	RunnerWorkDir string   `yaml:"runner_work_dir" doc:"working directory for the runner on the VM"`
	RunnerName    string   `yaml:"name"`
	Labels        []string `yaml:"labels,flow"`
	RunnerGroup   string   `yaml:"runner_group"`
	RepoURL       string   `yaml:"-"`
	Token         string   `yaml:"-"`
	Replace       bool     `yaml:"-"`
	Ephemeral     bool     `yaml:"-"`
}

func (rc GitHubRunnerConfig) ConfigCommandLine() string {
	var out strings.Builder
	fmt.Fprintf(&out, `cd %s && ./config.sh `, rc.RunnderDir)
	out.WriteString("--unattended ")
	if rc.Ephemeral {
		out.WriteString("--ephemeral ")
	}
	if rc.Replace {
		out.WriteString("--replace ")
	}
	fmt.Fprintf(&out, `--url %s --name %s --labels %s`,
		rc.RepoURL, rc.RunnerName, strings.Join(rc.Labels, ","))
	return out.String()
}

type VMPoolConfig struct {
	Name      string `yaml:"name" doc:"name of the VM pool (used for referencing in runner configs)"`
	Type      string `yaml:"type" doc:"type of VM pool (e.g. 'tart', 'parallels')"`
	Image     string `yaml:"image" doc:"base image to use for cloning VMs in this pool"`
	Size      int    `yaml:"size" doc:"number of VMs to maintain in this pool"`
	RunnerDir string `yaml:"runner_dir" doc:"directory on the VM in which the runner was installed, specific to each type of image."`
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
	DeliveryURL string   `yaml:"delivery_url" doc:"the URL to which GitHub will send webhook events"`
	Events      []string `yaml:"events" doc:"list of events to trigger the webhook on"`
	SecretID    string   `yaml:"secret_id" doc:"the keychain item name containing the webhook secret for HMAC signature verification"`
	SecretUser  string   `yaml:"secret_user" doc:"the user associated with the keychain item containing the webhook secret"`
}
