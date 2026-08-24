// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"strings"
	"testing"

	"cloudeng.io/webapi/clients/github/githubcmd"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubclient"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
)

func testRepo(repo string, runners ...githubclient.RunnerConfig) githubclient.RepositoryConfig {
	var rc githubclient.RepositoryConfig
	rc.Service = githubcmd.Service{Owner: "cloudengio", Repo: repo, PerPage: 30}
	rc.Runners = runners
	return rc
}

func testRunner(prefix, pool string, labels ...string) githubclient.RunnerConfig {
	return githubclient.RunnerConfig{NamePrefix: prefix, VMPoolName: pool, Labels: labels}
}

func testPools(names ...string) map[string]vmsclient.PoolConfig {
	pools := make(map[string]vmsclient.PoolConfig, len(names))
	for _, n := range names {
		pools[n] = vmsclient.PoolConfig{Tart: &vmsclient.TartConfig{
			Image:     n + "-ci",
			RunnerDir: "/Users/admin/actions-runner",
		}}
	}
	return pools
}

// TestConfigValidate covers the cross-checks between the repository, runner and
// VM pool sections, which are what a hand-edited configuration gets wrong.
func TestConfigValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			"valid",
			Config{
				Repositories: []githubclient.RepositoryConfig{
					testRepo("go.pkgs", testRunner("go-macos", "macos", "self-hosted", "macos")),
				},
				VMPools: testPools("macos"),
			},
			"",
		},
		{
			"runner names an undefined pool",
			Config{
				Repositories: []githubclient.RepositoryConfig{
					testRepo("go.pkgs", testRunner("go-macos", "windows", "self-hosted", "windows")),
				},
				VMPools: testPools("macos"),
			},
			`vm_pool "windows" not defined`,
		},
		{
			"invalid repository",
			Config{
				Repositories: []githubclient.RepositoryConfig{testRepo("go.pkgs")},
				VMPools:      testPools("macos"),
			},
			"at least one runner configuration is required",
		},
		{
			"pool with no backend",
			Config{
				Repositories: []githubclient.RepositoryConfig{
					testRepo("go.pkgs", testRunner("go-macos", "macos", "macos")),
				},
				VMPools: map[string]vmsclient.PoolConfig{"macos": {}},
			},
			"vm_pool macos",
		},
		{
			"web ui bound to a non-loopback address",
			Config{
				WebUI:   WebUIConfig{Enabled: true, ListenAddress: "0.0.0.0:8088"},
				VMPools: testPools("macos"),
			},
			"must resolve to 127.0.0.1",
		},
		{
			// The address is only checked when the web UI is enabled.
			"web ui disabled",
			Config{
				WebUI:   WebUIConfig{Enabled: false, ListenAddress: "0.0.0.0:8088"},
				VMPools: testPools("macos"),
			},
			"",
		},
		{
			// No repositories and no pools is vacuously valid; the run command
			// rejects an unusable configuration later, with a better message.
			"empty",
			Config{},
			"",
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
}

// TestConfigContext covers the round trip the subcommands rely on to reach the
// configuration parsed by main.
func TestConfigContext(t *testing.T) {
	if _, ok := ConfigFromContext(context.Background()); ok {
		t.Error("a bare context reported a configuration")
	}
	want := Config{Global: GlobalConfig{TmpDir: "orchestrator"}}
	got, ok := ConfigFromContext(ContextWithConfig(context.Background(), want))
	if !ok {
		t.Fatal("the configuration was not found in the context")
	}
	if got.Global.TmpDir != want.Global.TmpDir {
		t.Errorf("got %+v, want %+v", got.Global, want.Global)
	}
}

// TestLookupRepositoryConfig covers the selection the github subcommands make
// from --repo and --owner, including the ambiguity that --owner resolves.
func TestLookupRepositoryConfig(t *testing.T) {
	cloudeng := testRepo("core", testRunner("a", "macos", "macos"))
	other := testRepo("core", testRunner("b", "macos", "linux"))
	other.Service.Owner = "onyourbehalf"
	cfg := Config{Repositories: []githubclient.RepositoryConfig{cloudeng, other}}

	for _, tc := range []struct {
		name      string
		flags     GitHubFlags
		wantOwner string
		wantErr   string
	}{
		{"repo only takes the first match", GitHubFlags{Repo: "core"}, "cloudengio", ""},
		{"owner disambiguates", GitHubFlags{Repo: "core", Owner: "onyourbehalf"}, "onyourbehalf", ""},
		{"no repo", GitHubFlags{}, "", "--repo is required"},
		{"unknown repo", GitHubFlags{Repo: "nope"}, "", "no matching repository"},
		{"unknown owner", GitHubFlags{Repo: "core", Owner: "nobody"}, "", "no matching repository"},
	} {
		got, err := LookupRepositoryConfig(cfg, tc.flags)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%v: got %v, want it to mention %q", tc.name, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v: got %v, want nil", tc.name, err)
			continue
		}
		if got.Service.Owner != tc.wantOwner {
			t.Errorf("%v: owner: got %q, want %q", tc.name, got.Service.Owner, tc.wantOwner)
		}
	}
}
