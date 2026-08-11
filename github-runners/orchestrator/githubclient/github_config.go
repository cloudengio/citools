// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"cloudeng.io/webapi/clients/github/githubcmd"
	"cloudeng.io/webapi/operations/apicrawlcmd"
	"gopkg.in/yaml.v3"
)

type RunnerConfig struct {
	NamePrefix string `yaml:"name_prefix" doc:"prefix for name of the runner, must be unique within runners and will be suffixed with a timestamp and incrementing number to ensure uniqueness"`

	VMPoolName string `yaml:"vm_pool" doc:"name of the VM pool to use for this runner"`

	//RunnderDir    string   `yaml:"runner_dir" doc:"directory on the VM to install the runner"`
	//RunnerWorkDir string   `yaml:"runner_work_dir" doc:"working directory for the runner on the VM"`
	//RunnerGroup   string   `yaml:"runner_group"`

	Labels []string `yaml:"labels,flow" doc:"labels to assign to the runner, used to match runner to webhook events"`

	Replace   bool          `yaml:"replace" doc:"if true, replace any existing runner with the same name"`
	Ephemeral bool          `yaml:"ephemeral" doc:"if true, register the runner as ephemeral"`
	Timeout   time.Duration `yaml:"timeout" doc:"maximum time to wait for the runner to complete a job before it is considered failed and terminated"`
}

func (rc RunnerConfig) Validate() error {
	if len(rc.NamePrefix) == 0 {
		return fmt.Errorf("runner with vm_pool %s, and with labels %v, missing name prefix", rc.VMPoolName, rc.Labels)
	}
	if len(rc.Labels) == 0 {
		return fmt.Errorf("runner %s: at least one label is required", rc.NamePrefix)
	}
	if rc.VMPoolName == "" {
		return fmt.Errorf("runner %s: missing vm_pool", rc.NamePrefix)
	}
	return nil
}

type RepositoryConfig struct {
	apicrawlcmd.Crawl[githubcmd.Service] `yaml:",inline"`
	Runners                              []RunnerConfig `yaml:"runners" doc:"list of runner configurations for this repository"`
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
		Owner                                string         `yaml:"owner"`
		Organization                         string         `yaml:"organization"`
		Repo                                 string         `yaml:"repo"`
		PerPage                              int            `yaml:"per_page"`
		APIKeyID                             string         `yaml:"api_key_id"`
		Runners                              []RunnerConfig `yaml:"runners"`
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
				rc.Service.Repo, prev, runner.NamePrefix, sortedUniqueLabels(runner.Labels))
		}
		seen[key] = runner.NamePrefix
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
