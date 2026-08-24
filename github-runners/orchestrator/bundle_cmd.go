// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"

	"cloudeng.io/macos/buildtools"
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
	// keys are defaulted. It is a free-form map so callers need only set what
	// they care about.
	Info map[string]any `yaml:"info_plist"`
	// Signing holds the code-signing identity, entitlements and codesign args.
	Signing buildtools.SigningConfig `yaml:"signing"`
	// Notary holds the credentials used to notarize the signed bundle with
	// Apple's notarization service. Only used when --notarize is set.
	Notary buildtools.NotaryConfig `yaml:"notary"`
	// ProvisioningProfile is embedded as Contents/embedded.provisionprofile.
	ProvisioningProfile string `yaml:"provisioning_profile"`
	// OrchestratorConfig is the minimal orchestrator config file embedded into
	// the bundle as the installed default. Defaults to minimal_config.yml.
	OrchestratorConfig string `yaml:"orchestrator_config"`
	// LaunchAgentConfig is the launchd login service configuration embedded
	// into the bundle, which `service install` reads from there. Defaults to
	// launch_agent.yml.
	LaunchAgentConfig string `yaml:"launch_agent_config"`
}

// The bundle is a launcher app that wraps the orchestrator:
//
//	github-runner-orchestrator.app/                 (outer app, no entitlements)
//	  Contents/MacOS/github-runner-orchestrator-launcher   <- CFBundleExecutable
//	  Contents/MacOS/github-runner-orchestrator.app/       <- nested orchestrator
//	    Contents/MacOS/github-runner-orchestrator          <- keychain entitlement
//	    Contents/embedded.provisionprofile
//	  Contents/Resources/                                  <- config and service defaults
//
// The nested bundle must live in the outer bundle's Contents/MacOS, not
// Contents/Library: macosutils.InBundle, which the orchestrator and launcher
// use to find their way around, resolves a path to its enclosing bundle by way
// of a Contents/MacOS parent pair, so a bundle placed anywhere else cannot be
// resolved back to the app that contains it.
//
// The launcher (main executable) surfaces failures in a dialog and needs no
// entitlements or provisioning profile. The orchestrator carries the
// keychain-access-groups entitlement, which — being provisioning-profile
// restricted — only AMFI-authorizes for a bundle's own main executable; hence it
// is a nested bundle with its own embedded provisioning profile.
const (
	defaultExecutable  = internal.OrchestratorBinary
	launcherExecutable = internal.LauncherBinary
	launcherPackage    = "./cmd/orchestrator-launcher"

	// orchestratorBundleID is the nested bundle's CFBundleIdentifier; it matches
	// the App ID / provisioning profile carrying the keychain entitlement.
	orchestratorBundleID = internal.BundleID
	// outerBundleID is the default identifier of the outer launcher app. It must
	// differ from the nested bundle's id and needs no provisioning profile.
	outerBundleID = internal.OuterBundleID
	// nestedOrchestratorApp is the nested bundle path within the outer app's
	// Contents directory.
	nestedOrchestratorDir = "MacOS"
	nestedOrchestratorApp = internal.NestedOrchestratorApp
)

// bundledConfigName is the fixed name the orchestrator config is stored under in
// the bundle's Resources directory, so the app-launch code can locate it
// regardless of the source config's filename.
const bundledConfigName = internal.ConfigFileName

// BundleCommand builds a signed macOS .app bundle that installs the orchestrator.
type BundleCommand struct{}

type BundleFlags struct {
	VerboseFlags
	Config   string `subcmd:"config,installer.yaml,path to the installer bundle configuration file"`
	Binary   string `subcmd:"binary,,path to a prebuilt orchestrator binary; if empty the current package (.) is built"`
	Timing   bool   `subcmd:"timing,false,print timing information for each build step"`
	DryRun   bool   `subcmd:"dry-run,false,print the build steps without executing them"`
	Notarize bool   `subcmd:"notarize,false,submit the signed bundle to Apple for notarization and staple the ticket"`
}

func (BundleCommand) Run(ctx context.Context, fl any, _ []string) error {
	fv := fl.(*BundleFlags)

	cfg, err := loadBundleConfig(fv.Config)
	if err != nil {
		return err
	}

	// The outer app runs the launcher; the nested bundle runs the orchestrator.
	outerInfo, err := buildInfoPlist(cfg.Info, launcherExecutable, outerBundleID)
	if err != nil {
		return err
	}
	innerInfo, err := buildInfoPlist(nil, defaultExecutable, orchestratorBundleID)
	if err != nil {
		return err
	}

	orchestrator := fv.Binary
	if orchestrator == "" {
		b, cleanup, err := buildBinary(ctx, ".")
		if err != nil {
			return err
		}
		defer cleanup()
		orchestrator = b
	}

	launcher, cleanup, err := buildBinary(ctx, launcherPackage)
	if err != nil {
		return err
	}
	defer cleanup()

	fmt.Printf("building app bundle %s with orchestrator binary %s (dry run: %v)\n", cfg.Bundle, orchestrator, fv.DryRun)

	return buildAppBundle(ctx, cfg, outerInfo, innerInfo, launcher, orchestrator, fv.Timing, fv.stepsVerbose(), fv.DryRun, fv.Notarize)
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
func buildInfoPlist(user map[string]any, executable, bundleID string) (buildtools.InfoPlist, error) {
	raw := map[string]any{
		"CFBundleExecutable":     executable,
		"CFBundleName":           defaultExecutable,
		"CFBundleDisplayName":    "GitHub Runner Orchestrator",
		"CFBundleIdentifier":     bundleID,
		"CFBundlePackageType":    "APPL",
		"CFBundleVersion":        "0.0.0",
		"LSMinimumSystemVersion": "15.0", // macOS Sequoia
	}
	maps.Copy(raw, user)
	merged, err := yaml.Marshal(raw)
	if err != nil {
		return buildtools.InfoPlist{}, err
	}
	var info buildtools.InfoPlist
	if err := yaml.Unmarshal(merged, &info); err != nil {
		return buildtools.InfoPlist{}, fmt.Errorf("building Info.plist: %w", err)
	}
	// Required keys are no longer checked while unmarshalling, since an
	// InfoPlist may also describe a launchd job, so check them here.
	if err := info.Validate(); err != nil {
		return buildtools.InfoPlist{}, fmt.Errorf("building Info.plist: %w", err)
	}
	return info, nil
}

// buildBinary builds the Go package pkg into a temporary file, returning its
// path and a cleanup function.
func buildBinary(ctx context.Context, pkg string) (string, func(), error) {
	tmp, err := os.CreateTemp("", "orchestrator-build-*")
	if err != nil {
		return "", nil, err
	}
	_ = tmp.Close()
	cleanup := func() { _ = os.Remove(tmp.Name()) }
	cmd := exec.CommandContext(ctx, "go", "build", "-o", tmp.Name(), pkg)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("building %s: %w", pkg, err)
	}
	return tmp.Name(), cleanup, nil
}

// buildAppBundle assembles and (if an identity is configured) signs the outer
// launcher app and its nested orchestrator bundle using cloudeng.io/macos/buildtools.
func buildAppBundle(ctx context.Context, cfg BundleConfig, outerInfo, innerInfo buildtools.InfoPlist, launcher, orchestrator string, timing, verbose, dryRun, notarize bool) error {
	if notarize && cfg.Signing.Identity == "" {
		return fmt.Errorf("--notarize requires a signing identity: set signing.identity in the bundle config")
	}
	if cfg.Signing.Identity != "" && cfg.Signing.Entitlements != nil {
		if cfg.ProvisioningProfile == "" {
			return fmt.Errorf("signing with entitlements requires a provisioning profile: set provisioning_profile in the bundle config")
		}
	}
	outer := buildtools.AppBundle{Path: cfg.Bundle, Info: outerInfo}
	inner := buildtools.AppBundle{
		Path: outer.Contents(nestedOrchestratorDir, nestedOrchestratorApp),
		Info: innerInfo,
	}

	var stepOpts []buildtools.StepRunnerOption
	if timing {
		stepOpts = append(stepOpts, buildtools.WithStepTiming(true))
	}
	if verbose {
		stepOpts = append(stepOpts, buildtools.WithStepVerbose(true))
	}
	runner := buildtools.NewRunner(stepOpts...)

	runner.AddSteps(outer.Clean())
	runner.AddSteps(outer.Create()...)

	// Nested orchestrator bundle: the executable that holds the keychain
	// entitlement, authorized by its own embedded provisioning profile.
	runner.AddSteps(inner.Create()...)
	runner.AddSteps(inner.WriteInfoPlist(), inner.CopyExecutable(orchestrator))
	if cfg.ProvisioningProfile != "" {
		profilePath := os.ExpandEnv(cfg.ProvisioningProfile)
		if _, err := os.Stat(profilePath); err != nil {
			return fmt.Errorf("provisioning profile %q is not accessible: %w", profilePath, err)
		}
		runner.AddSteps(inner.InstallProvisioningProfile(profilePath))
	}

	// Outer launcher app.
	runner.AddSteps(
		outer.WriteInfoPlist(),
		outer.CopyExecutable(launcher),
		outer.CopyContents(cfg.OrchestratorConfig, "Resources", bundledConfigName),
		outer.CopyContents(cfg.LaunchAgentConfig, "Resources", internal.LaunchAgentFileName),
	)

	if cfg.Signing.Identity != "" {
		// The orchestrator is signed with the keychain entitlement; the launcher
		// needs none. Sign the nested bundle fully first, then the launcher, then
		// seal the outer bundle last so each signature seals what it contains.
		entitled := cfg.Signing.Signer()
		plain := buildtools.NewSigner(cfg.Signing.Identity, nil, nil, cfg.Signing.CodesignArguments)
		runner.AddSteps(
			inner.SignExecutable(entitled),
			inner.Sign(entitled),
			outer.SignExecutable(plain),
			outer.Sign(plain),
		)
	}
	// Notarization must follow signing: Apple staples a ticket into the already
	// signed bundle so Gatekeeper accepts it on other Macs.
	if notarize {
		runner.AddSteps(outer.Notarize(cfg.Notary)...)
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
