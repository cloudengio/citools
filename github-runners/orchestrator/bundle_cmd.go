// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"cloudeng.io/macos/buildtools"
	"cloudeng.io/os/executil"
	"gopkg.in/yaml.v3"

	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
)

// BundleConfig is the installer-specific configuration read from the file passed
// to `orchestrator bundle`. It carries the external, developer-supplied values
// (signing identity, entitlements, provisioning profile, bundle metadata) plus
// which orchestrator config to embed as the installed default. It is deliberately
// separate from the orchestrator's own runtime configuration.
type BundleConfig struct {
	// Bundle is the output .app path; defaults to <CFBundleExecutable>.app.
	Bundle string `yaml:"bundle"`
	// Info holds Info.plist fields (e.g. CFBundleIdentifier); missing standard
	// keys are defaulted.
	Info buildtools.InfoPlist `yaml:"info_plist"`
	// Signing holds the code-signing identity, entitlements and codesign args.
	Signing buildtools.SigningConfig `yaml:"signing"`
	// Permissions holds the permissions for the bundle executables and directories.
	Permissions buildtools.PermissionsConfig `yaml:"permissions,omitempty"`
	// Notary holds the credentials used to notarize the signed bundle with
	// Apple's notarization service. Only used when --notarize is set.
	Notary buildtools.NotaryConfig `yaml:"notary"`
	// ProvisioningProfile is embedded as Contents/embedded.provisionprofile.
	ProvisioningProfile string `yaml:"provisioning_profile"`
	// OrchestratorConfig is the minimal orchestrator config file embedded into
	// the bundle as the installed default. Defaults to minimal_config.yml.
	OrchestratorConfig string `yaml:"orchestrator_config"`
	// Version is the human-readable release version stamped into the bundle
	// as CFBundleShortVersionString. Defaults to DefaultVersion.
	Version string `yaml:"version"`
	// LaunchAgentConfig is the launchd login service configuration embedded
	// into the bundle, which `service install` reads from there. Defaults to
	// launch_agent.yml.
	LaunchAgentConfig string `yaml:"launch_agent_config"`
}

// The bundle packages the orchestrator as a standalone macOS application:
//
//	github-runner-orchestrator.app/
//	  Contents/MacOS/github-runner-orchestrator          <- CFBundleExecutable (with entitlements)
//	  Contents/embedded.provisionprofile
//	  Contents/Resources/                                <- config and service defaults
//
// The single binary handles both standalone app launches (with a Dock icon and
// menu bar status item) and background service execution (status item only).
const (
	defaultExecutable    = internal.OrchestratorBinary
	orchestratorBundleID = internal.BundleID
)

// bundledConfigName is the fixed name the orchestrator config is stored under in
// the bundle's Resources directory, so the app-launch code can locate it
// regardless of the source config's filename.
const bundledConfigName = internal.ConfigFileName

// BundleCommand builds a signed macOS .app bundle that installs the orchestrator.
type BundleCommand struct{}

type BundleFlags struct {
	VerboseFlags
	Config     string `subcmd:"config,installer.yaml,path to the installer bundle configuration file"`
	Binary     string `subcmd:"binary,,path to a prebuilt orchestrator binary; if empty the current package (.) is built"`
	Timing     bool   `subcmd:"timing,false,print timing information for each build step"`
	DryRun     bool   `subcmd:"dry-run,false,print the build steps without executing them"`
	Notarize   bool   `subcmd:"notarize,false,submit the signed bundle to Apple for notarization and staple the ticket"`
	AllowDirty      bool   `subcmd:"allow-dirty,false,'build even though the tree has uncommitted changes; the bundle is then stamped -dirty'"`
	SkipWebappBuild bool   `subcmd:"skip-webapp-build,false,skip building the web UI frontend before compiling the binary"`
}

func (BundleCommand) Run(ctx context.Context, fl any, _ []string) error {
	fv := fl.(*BundleFlags)

	cfg, err := loadBundleConfig(fv.Config)
	if err != nil {
		return err
	}

	// The bundle is built from this tree, so it carries the version derived from git.
	version, err := gitVersion(ctx, ".", cfg.Version)
	if err != nil {
		return err
	}
	if version.Dirty && !fv.AllowDirty {
		return fmt.Errorf("the tree has uncommitted changes, so the bundle would claim commit %s without matching it; commit them or pass --allow-dirty", version.Commit)
	}
	fmt.Printf("version %s (commit %s)\n", version.Build, version.Commit)

	info, err := buildInfoPlist(cfg.Info, defaultExecutable, orchestratorBundleID, version)
	if err != nil {
		return err
	}

	buildEnv := buildtools.GoBuildEnvForMacOSVersion(info.LSMinimumSystemVersion)

	orchestrator := fv.Binary
	if orchestrator == "" {
		if !fv.SkipWebappBuild && !fv.DryRun {
			frontendDir := filepath.Join("webui", "frontend")
			if err := buildWebapp(ctx, frontendDir, false, false); err != nil {
				return fmt.Errorf("building web UI frontend: %w", err)
			}
		}
		b, cleanup, err := buildBinary(ctx, ".", buildEnv...)
		if err != nil {
			return err
		}
		defer cleanup()
		orchestrator = b
	}

	fmt.Printf("building app bundle %s with orchestrator binary %s (dry run: %v)\n", cfg.Bundle, orchestrator, fv.DryRun)

	return buildAppBundle(ctx, cfg, info, orchestrator, fv.Timing, fv.stepsVerbose(), fv.DryRun, fv.Notarize)
}

func loadBundleConfig(path string) (BundleConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BundleConfig{}, fmt.Errorf("reading bundle config %s: %w", path, err)
	}
	// Expand ${ENV} references in every string value so that sensitive or
	// machine-specific values (signing identity, team ID) can be kept out of the
	// checked-in config file.
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return BundleConfig{}, fmt.Errorf("parsing bundle config %s: %w", path, err)
	}
	expandEnv(raw)
	expanded, err := yaml.Marshal(raw)
	if err != nil {
		return BundleConfig{}, err
	}
	var cfg BundleConfig
	if err := yaml.Unmarshal(expanded, &cfg); err != nil {
		return BundleConfig{}, fmt.Errorf("parsing bundle config %s: %w", path, err)
	}
	if cfg.Bundle == "" {
		cfg.Bundle = defaultExecutable + ".app"
	}
	if cfg.OrchestratorConfig == "" {
		cfg.OrchestratorConfig = "minimal_config.yml"
	}
	if cfg.LaunchAgentConfig == "" {
		cfg.LaunchAgentConfig = internal.LaunchAgentFileName
	}
	if cfg.Version == "" {
		cfg.Version = DefaultVersion
	}
	return cfg, nil
}

// expandEnv walks a decoded YAML value and expands ${ENV} references in every
// string, in place.
func expandEnv(v any) any {
	switch t := v.(type) {
	case string:
		return os.ExpandEnv(t)
	case map[string]any:
		for k, val := range t {
			t[k] = expandEnv(val)
		}
	case []any:
		for i, val := range t {
			t[i] = expandEnv(val)
		}
	}
	return v
}

// buildInfoPlist merges the caller-supplied Info.plist keys over the standard
// defaults required for a launchable bundle with the given main executable and
// bundle identifier, and produces a buildtools.InfoPlist.
func buildInfoPlist(user buildtools.InfoPlist, executable, bundleID string, version versionInfo) (buildtools.InfoPlist, error) {
	info := user.WithDefaults(executable)
	if user.CFBundleName == "" {
		info.CFBundleName = defaultExecutable
	}
	if user.CFBundleDisplayName == "" {
		info.CFBundleDisplayName = "GitHub Runner Orchestrator"
	}
	if user.CFBundleIdentifier == "" {
		info.CFBundleIdentifier = bundleID
	}
	if user.LSMinimumSystemVersion == "" {
		info.LSMinimumSystemVersion = "15.0" // macOS Sequoia
	}
	if user.CFBundleShortVersionString == "" {
		info.CFBundleShortVersionString = version.Short
	}
	if user.CFBundleVersion == "" {
		info.CFBundleVersion = version.Build
	}
	if info.Extra == nil {
		info.Extra = make(map[string]any)
	}
	if _, ok := info.Extra["CGCommit"]; !ok {
		info.Extra["CGCommit"] = version.Commit
	}
	if _, ok := info.Extra["CGBuildTime"]; !ok {
		info.Extra["CGBuildTime"] = version.BuildTime.UTC().Format(time.RFC3339)
	}
	if err := info.Validate(); err != nil {
		return buildtools.InfoPlist{}, fmt.Errorf("building Info.plist: %w", err)
	}
	return info, nil
}

// buildBinary builds the Go package pkg into a temporary file, returning its
// path and a cleanup function.
func buildBinary(ctx context.Context, pkg string, env ...string) (string, func(), error) {
	tmp, err := os.CreateTemp("", "orchestrator-build-*")
	if err != nil {
		return "", nil, err
	}
	_ = tmp.Close()
	cleanup := func() { _ = os.Remove(tmp.Name()) }

	gobin, gobinargs, err := executil.GoBuildArgs(tmp.Name(), pkg)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("getting Go build args: %w", err)
	}
	cmd := exec.CommandContext(ctx, gobin, gobinargs...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("building %s: %w", pkg, err)
	}
	return tmp.Name(), cleanup, nil
}

// buildAppBundle assembles and (if an identity is configured) signs the
// orchestrator app bundle using cloudeng.io/macos/buildtools.
func buildAppBundle(ctx context.Context, cfg BundleConfig, info buildtools.InfoPlist, orchestrator string, timing, verbose, dryRun, notarize bool) error {
	if notarize {
		if !cfg.Notary.Configured() {
			return fmt.Errorf("--notarize requires notary credentials: configure notary in the bundle config")
		}
		if err := cfg.Notary.ValidateSigning(cfg.Signing); err != nil {
			return err
		}
	}
	if cfg.Signing.Configured() && cfg.Signing.Entitlements != nil {
		if cfg.ProvisioningProfile == "" {
			return fmt.Errorf("signing with entitlements requires a provisioning profile: set provisioning_profile in the bundle config")
		}
	}
	app := buildtools.AppBundle{Path: cfg.Bundle, Info: info}

	var stepOpts []buildtools.StepRunnerOption
	if timing {
		stepOpts = append(stepOpts, buildtools.WithStepTiming(true))
	}
	if verbose {
		stepOpts = append(stepOpts, buildtools.WithStepVerbose(true))
	}
	runner := buildtools.NewRunner(stepOpts...)

	runner.AddSteps(app.Clean()...)
	runner.AddSteps(app.Create()...)

	runner.AddSteps(
		app.WriteInfoPlist(),
		app.CopyExecutable(orchestrator),
		app.CopyContents(cfg.OrchestratorConfig, "Resources", bundledConfigName),
		app.CopyContents(cfg.LaunchAgentConfig, "Resources", internal.LaunchAgentFileName),
		app.SetExecutablePermissions(orchestrator, cfg.Permissions.ExecutableMode()),
		app.SetMacOSDirPermissions(cfg.Permissions.MacOSDirMode()),
	)

	if cfg.ProvisioningProfile != "" {
		profilePath := os.ExpandEnv(cfg.ProvisioningProfile)
		if _, err := os.Stat(profilePath); err != nil {
			return fmt.Errorf("provisioning profile %q is not accessible: %w", profilePath, err)
		}
		runner.AddSteps(app.InstallProvisioningProfile(profilePath))
	}

	if cfg.Signing.Configured() {
		signer := cfg.Signing.Signer()
		runner.AddSteps(
			app.SignExecutable(signer),
			app.Sign(signer),
		)
	}

	// Notarization must follow signing: Apple staples a ticket into the already
	// signed bundle so Gatekeeper accepts it on other Macs.
	if notarize {
		runner.AddSteps(app.Notarize(cfg.Notary)...)
	}

	results := runner.Run(ctx, buildtools.NewCommandRunner(buildtools.WithDryRun(dryRun)))
	for _, r := range results {
		if r.Error() != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n%s\n", r.CommandLine(), r.Error(), r.Output())
		}
	}
	if err := results.Error(); err != nil {
		return err
	}
	fmt.Printf("created app bundle %s\n", cfg.Bundle)
	return nil
}
