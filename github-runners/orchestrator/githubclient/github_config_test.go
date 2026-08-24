// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"strings"
	"testing"

	"cloudeng.io/webapi/clients/github/githubcmd"
	"gopkg.in/yaml.v3"
)

func runner(prefix, pool string, labels ...string) RunnerConfig {
	return RunnerConfig{NamePrefix: prefix, VMPoolName: pool, Labels: labels}
}

func TestRunnerConfigValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     RunnerConfig
		wantErr string
	}{
		{"valid", runner("go-macos", "macos", "self-hosted", "macos"), ""},
		{"no name prefix", runner("", "macos", "macos"), "missing name prefix"},
		{"no labels", runner("go-macos", "macos"), "at least one label is required"},
		{"no vm pool", runner("go-macos", "", "macos"), "missing vm_pool"},
	} {
		err := tc.cfg.Validate()
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%v: got %v, want nil", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%v: got %v, want it to mention %q", tc.name, err, tc.wantErr)
		}
	}
}

// repoConfig returns a valid repository configuration with the given runners.
func repoConfig(runners ...RunnerConfig) RepositoryConfig {
	var rc RepositoryConfig
	rc.Service = githubcmd.Service{Owner: "cloudengio", Repo: "go.pkgs", PerPage: 30}
	rc.Runners = runners
	return rc
}

func TestRepositoryConfigValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     RepositoryConfig
		wantErr string
	}{
		{
			"valid",
			repoConfig(runner("go-macos", "macos", "self-hosted", "macos"),
				runner("go-linux", "linux", "self-hosted", "linux")),
			"",
		},
		{"no runners", repoConfig(), "at least one runner configuration is required"},
		{
			"invalid runner",
			repoConfig(runner("go-macos", "", "macos")),
			"missing vm_pool",
		},
	} {
		err := tc.cfg.Validate()
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%v: got %v, want nil", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%v: got %v, want it to mention %q", tc.name, err, tc.wantErr)
		}
	}

	// A repository with no service configuration is rejected before its runners
	// are considered.
	var empty RepositoryConfig
	empty.Runners = []RunnerConfig{runner("go-macos", "macos", "macos")}
	if err := empty.Validate(); err == nil {
		t.Error("a repository with no owner or repo was accepted")
	}
}

// TestValidateUniqueLabels covers the rule that makes routing deterministic:
// a webhook selects a runner by its label set, so two runners in a repository
// may not share one. Labels are compared as a set, so order, duplicates and
// case must not let a collision through.
func TestValidateUniqueLabels(t *testing.T) {
	for _, tc := range []struct {
		name      string
		runners   []RunnerConfig
		colliding bool
	}{
		{"distinct", []RunnerConfig{
			runner("a", "p", "self-hosted", "macos"),
			runner("b", "p", "self-hosted", "linux"),
		}, false},
		{"identical", []RunnerConfig{
			runner("a", "p", "self-hosted", "macos"),
			runner("b", "p", "self-hosted", "macos"),
		}, true},
		{"reordered", []RunnerConfig{
			runner("a", "p", "self-hosted", "macos"),
			runner("b", "p", "macos", "self-hosted"),
		}, true},
		{"differing case", []RunnerConfig{
			runner("a", "p", "self-hosted", "macOS"),
			runner("b", "p", "SELF-HOSTED", "macos"),
		}, true},
		{"duplicates within one set", []RunnerConfig{
			runner("a", "p", "macos", "macos"),
			runner("b", "p", "macos"),
		}, true},
		{"one a subset of the other", []RunnerConfig{
			runner("a", "p", "self-hosted", "macos"),
			runner("b", "p", "self-hosted"),
		}, false},
	} {
		err := repoConfig(tc.runners...).Validate()
		if tc.colliding {
			if err == nil || !strings.Contains(err.Error(), "share the same set of labels") {
				t.Errorf("%v: got %v, want a label collision error", tc.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v: got %v, want nil", tc.name, err)
		}
	}
}

// TestRepositoryConfigUnmarshalYAML covers the flat layout the orchestrator's
// configuration file uses, which the embedded Crawl type does not accept on its
// own, and the defaulting of per_page.
func TestRepositoryConfigUnmarshalYAML(t *testing.T) {
	const spec = `
owner: cloudengio
repo: go.pkgs
api_key_id: github-token
user_id: cnicolaou
runners:
  - name_prefix: go-macos
    vm_pool: macos
    labels: [self-hosted, macos]
`
	var rc RepositoryConfig
	if err := yaml.Unmarshal([]byte(spec), &rc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, want := rc.Service.Owner, "cloudengio"; got != want {
		t.Errorf("owner: got %q, want %q", got, want)
	}
	if got, want := rc.Service.Repo, "go.pkgs"; got != want {
		t.Errorf("repo: got %q, want %q", got, want)
	}
	if got, want := rc.KeyID, "github-token"; got != want {
		t.Errorf("key id: got %q, want %q", got, want)
	}
	if got, want := rc.UserID, "cnicolaou"; got != want {
		t.Errorf("user id: got %q, want %q", got, want)
	}
	// per_page is not given, so it must default rather than be left at zero,
	// which Service.Validate would reject.
	if got, want := rc.Service.PerPage, githubcmd.DefaultPageSize; got != want {
		t.Errorf("per page: got %v, want %v", got, want)
	}
	if got, want := len(rc.Runners), 1; got != want {
		t.Fatalf("runners: got %d, want %d", got, want)
	}
	if err := rc.Validate(); err != nil {
		t.Errorf("the unmarshalled configuration does not validate: %v", err)
	}

	// An explicit per_page is honoured.
	var withPage RepositoryConfig
	if err := yaml.Unmarshal([]byte(spec+"per_page: 100\n"), &withPage); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, want := withPage.Service.PerPage, 100; got != want {
		t.Errorf("per page: got %v, want %v", got, want)
	}
}

// TestCanonicalLabelSet covers the key that label matching depends on: two
// label sets collide exactly when they contain the same labels.
func TestCanonicalLabelSet(t *testing.T) {
	same := [][2][]string{
		{{"a", "b"}, {"b", "a"}},
		{{"A", "b"}, {"a", "B"}},
		{{"a", "a", "b"}, {"a", "b"}},
		{{}, {}},
	}
	for _, tc := range same {
		if got, want := canonicalLabelSet(tc[0]), canonicalLabelSet(tc[1]); got != want {
			t.Errorf("canonicalLabelSet(%v) = %q, canonicalLabelSet(%v) = %q, want equal", tc[0], got, tc[1], want)
		}
	}
	differ := [][2][]string{
		{{"a"}, {"b"}},
		{{"a"}, {"a", "b"}},
		// Quoting keeps a comma in a label from forging another set.
		{{"a,b"}, {"a", "b"}},
	}
	for _, tc := range differ {
		if got, want := canonicalLabelSet(tc[0]), canonicalLabelSet(tc[1]); got == want {
			t.Errorf("canonicalLabelSet(%v) and canonicalLabelSet(%v) both = %q, want different", tc[0], tc[1], got)
		}
	}
}

func TestSortedUniqueLabels(t *testing.T) {
	got := sortedUniqueLabels([]string{"Zebra", "apple", "APPLE", "mango"})
	want := []string{"apple", "mango", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
			break
		}
	}
}
