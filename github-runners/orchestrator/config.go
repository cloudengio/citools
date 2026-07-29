package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"cloudeng.io/cmdutil"
	"cloudeng.io/file/crawl/crawlcmd"
	macoskeychain "cloudeng.io/macos/keychain/plugin"
	"cloudeng.io/webapi/clients/github/githubcmd"
	"cloudeng.io/webapi/operations"
	"cloudeng.io/webapi/operations/apicrawlcmd"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Logging        cmdutil.LoggingConfig           `yaml:"logging" doc:"logging configuration options"`
	TmpDir         string                          `yaml:"tmp_dir" doc:"directory in which to create temporary files and directories"`
	Repositories   []RepositoryConfig              `yaml:"repositories" doc:"list of GitHub repositories to manage runners for"`
	ICloudKeychain ICloudKeychainConfig            `yaml:"icloud_keychain" doc:"icloud keychain configuration for loading API keys"`
	VMPools        map[string]vmsclient.PoolConfig `yaml:"vm_pools" doc:"configuration for VM pools to use for runner provisioning"`
	Webhook        WebhookConfig                   `yaml:"webhook" doc:"configuration for the GitHub webhook relay service"`

	// KeepFailedDuration time.Duration `yaml:"keep_failed_duration"`
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
					repo.Service.Repo, runner.Name, runner.VMPoolName)
			}
		}
	}
	return nil
}

// sortedUniqueLabels returns the labels lower-cased, de-duplicated, and sorted,
// giving a canonical ordering for a label set.
func sortedUniqueLabels(labels []string) []string {
	set := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		set[strings.ToLower(l)] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// canonicalLabelSet returns a string key that is identical for any two label
// slices representing the same set. Each label is quoted so that the joined key
// is unambiguous regardless of label contents.
func canonicalLabelSet(labels []string) string {
	uniq := sortedUniqueLabels(labels)
	for i, l := range uniq {
		uniq[i] = strconv.Quote(l)
	}
	return strings.Join(uniq, ",")
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
	if err := rc.validateUniqueLabels(); err != nil {
		return err
	}
	for _, runner := range rc.Runners {
		if err := runner.Validate(); err != nil {
			return fmt.Errorf("repository %s: %w", rc.Service.Repo, err)
		}
	}
	return nil
}

// validateUniqueLabels ensures that no two runners within this repository share
// the same set of labels. Labels are compared as a set via canonicalLabelSet, so
// ordering, duplicates, and case are ignored; each runner must be selectable by
// a distinct label set.
func (rc RepositoryConfig) validateUniqueLabels() error {
	seen := map[string]string{} // canonical label set -> name of the runner that first used it
	for _, runner := range rc.Runners {
		key := canonicalLabelSet(runner.Labels)
		if prev, ok := seen[key]; ok {
			return fmt.Errorf("repository %s: runners %s and %s share the same set of labels %v: each runner must have a unique set of labels",
				rc.Service.Repo, prev, runner.Name, sortedUniqueLabels(runner.Labels))
		}
		seen[key] = runner.Name
	}
	return nil
}

type GitHubRunnerConfig struct {
	Name       string `yaml:"name" doc:"name of the runner, must be unique within runners and may be used to register the runner with GitHub if a name is not available via a webhook event"`
	VMPoolName string `yaml:"vm_pool" doc:"name of the VM pool to use for this runner"`

	//RunnderDir    string   `yaml:"runner_dir" doc:"directory on the VM to install the runner"`
	//RunnerWorkDir string   `yaml:"runner_work_dir" doc:"working directory for the runner on the VM"`
	//RunnerGroup   string   `yaml:"runner_group"`

	Labels []string `yaml:"labels,flow" doc:"labels to assign to the runner, used to match runner to webhook events"`

	Replace   bool          `yaml:"replace" doc:"if true, replace any existing runner with the same name"`
	Ephemeral bool          `yaml:"ephemeral" doc:"if true, register the runner as ephemeral"`
	Timeout   time.Duration `yaml:"timeout" doc:"maximum time to wait for the runner to complete a job before it is considered failed and terminated"`
}

func (rc GitHubRunnerConfig) Validate() error {
	if len(rc.Name) == 0 {
		return fmt.Errorf("runner with vm_pool %s, and with labels %v, missing name", rc.VMPoolName, rc.Labels)
	}
	if len(rc.Labels) == 0 {
		return fmt.Errorf("runner %s: at least one label is required", rc.Name)
	}
	if rc.VMPoolName == "" {
		return fmt.Errorf("runner %s: missing vm_pool", rc.Name)
	}
	return nil
}

/*
func (rc GitHubRunnerConfig) ConfigCommandLine(fullRepoName string, token string) string {
	var out strings.Builder
	fmt.Fprintf(&out, `cd %s && ./config.sh `, rc.RunnderDir)
	out.WriteString("--unattended ")
	if rc.Ephemeral {
		out.WriteString("--ephemeral ")
	}
	if rc.Replace {
		out.WriteString("--replace ")
	}
	if len(rc.RunnerWorkDir) > 0 {
		fmt.Fprintf(&out, `--work %s `, rc.RunnerWorkDir)
	}
	url := fmt.Sprintf("https://github.com/%s", fullRepoName)
	fmt.Fprintf(&out, `--url %s --name %s --labels %s --token %s`,
		url, rc.RunnerName, strings.Join(rc.Labels, ","), token)
	return out.String()
}
*/

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
