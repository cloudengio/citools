// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"cloudeng.io/cmdutil/cmdconfig"
	"cloudeng.io/macos/buildtools"
	"howett.net/plist"
)

// bundleFlag returns a flag holding the files, which are set, replacing the
// defaults, if there are any.
func bundleFlag(t *testing.T, files ...string) *cmdconfig.FilesFlag {
	t.Helper()
	var ff cmdconfig.FilesFlag
	ff.SetDefaults("bundle-builder.yaml", "bundle-builder.env")
	if len(files) > 0 {
		if err := ff.Set(strings.Join(files, ",")); err != nil {
			t.Fatal(err)
		}
	}
	return &ff
}

// TestBundleConfigEnv covers the ${ENV} expansion applied to the bundle
// configuration, which is how signing identities and profile paths are kept out
// of the file: from the environment, which takes precedence, and from the
// environment variables file.
func TestBundleConfigEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORCH_TEST_ID", "Developer ID Application: Example")
	t.Setenv("ORCH_TEST_UNSET", "")
	envFile := filepath.Join(dir, "bundle-builder.env")
	if err := os.WriteFile(envFile, []byte("ORCH_TEST_ID=from-file\nORCH_TEST_PROFILE=/profiles/p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "bundle-builder.yaml")
	content := `signing:
  identity: "${ORCH_TEST_ID}"
  entitlements:
    keychain-access-groups:
      - ${ORCH_TEST_UNSET}.group
provisioning_profile: ${ORCH_TEST_PROFILE}
notary:
  keychain_profile: "${ORCH_TEST_UNSET}"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, undefined, err := loadBundleConfig(t.Context(), bundleFlag(t, path, envFile))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ORCH_TEST_UNSET"}; !slices.Equal(undefined, want) {
		t.Errorf("undefined: got %v, want %v", undefined, want)
	}
	if want := "Developer ID Application: Example"; cfg.Signing.Identity != want {
		t.Errorf("identity: got %q, want %q", cfg.Signing.Identity, want)
	}
	if want := "/profiles/p"; cfg.ProvisioningProfile != want {
		t.Errorf("profile: got %q, want %q", cfg.ProvisioningProfile, want)
	}
	// A variable that is not defined is empty, so that, for example, an unset
	// signing identity leaves the bundle unsigned.
	if cfg.Notary.KeychainProfile != "" {
		t.Errorf("notary profile: got %q, want empty", cfg.Notary.KeychainProfile)
	}
}

// TestBuildInfoPlist covers the defaults every bundle needs and the caller's
// ability to override them, since a missing key makes an unlaunchable bundle.
func TestBuildInfoPlist(t *testing.T) {
	version := buildtools.Version{Short: "1.0.0", Build: "1.0.0+abcdef12", Commit: "abcdef12345", BuildTime: time.Now()}
	info, err := buildInfoPlist(buildtools.InfoPlist{}, "my-exe", "io.cloudeng.example", version)
	if err != nil {
		t.Fatalf("buildInfoPlist: %v", err)
	}
	if got, want := info.CFBundleExecutable, "my-exe"; got != want {
		t.Errorf("CFBundleExecutable: got %q, want %q", got, want)
	}
	if got, want := info.CFBundleIdentifier, "io.cloudeng.example"; got != want {
		t.Errorf("CFBundleIdentifier: got %q, want %q", got, want)
	}
	if info.CFBundlePackageType == "" || info.LSMinimumSystemVersion == "" || info.CFBundleVersion == "" {
		t.Errorf("a required key was left unset: %+v", info)
	}
	// The version is stamped from the build rather than hard-coded.
	if got, want := info.CFBundleShortVersionString, version.Short; got != want {
		t.Errorf("CFBundleShortVersionString: got %q, want %q", got, want)
	}
	if got, want := info.CFBundleVersion, version.Build; got != want {
		t.Errorf("CFBundleVersion: got %q, want %q", got, want)
	}
	if got, want := info.Extra["CGCommit"], version.Commit; got != want {
		t.Errorf("CGCommit: got %v, want %v", got, want)
	}
	if _, ok := info.Extra["CGBuildTime"]; !ok {
		t.Errorf("CGBuildTime was not recorded: %+v", info.Extra)
	}

	// User keys override the defaults and unknown keys are preserved, so that
	// a bundle can carry keys this package knows nothing about.
	info, err = buildInfoPlist(buildtools.InfoPlist{
		CFBundleVersion: "1.2.3",
		Extra: map[string]any{
			"NSHighResolutionCapable": true,
		},
	}, "my-exe", "io.cloudeng.example", version)
	if err != nil {
		t.Fatalf("buildInfoPlist: %v", err)
	}
	if got, want := info.CFBundleVersion, "1.2.3"; got != want {
		t.Errorf("CFBundleVersion: got %q, want %q", got, want)
	}
	data, err := plist.MarshalIndent(info, plist.XMLFormat, "\t")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<key>NSHighResolutionCapable</key>") {
		t.Errorf("an unknown key was dropped:\n%s", data)
	}

	// An empty bundle identifier is rejected rather than producing a bundle that
	// will not launch.
	if _, err := buildInfoPlist(buildtools.InfoPlist{}, "my-exe", "", version); err == nil {
		t.Error("an empty bundleID was accepted")
	}
}

// TestLoadBundleConfig covers the defaults applied to an installer
// configuration, which is what makes a minimal bundle-builder.yaml usable.
func TestLoadBundleConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle-builder.yaml")
	if err := os.WriteFile(path, []byte("info_plist:\n  CFBundleVersion: \"9.9\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := loadBundleConfig(t.Context(), bundleFlag(t, path))
	if err != nil {
		t.Fatalf("loadBundleConfig: %v", err)
	}
	if got, want := cfg.Path, defaultExecutable+".app"; got != want {
		t.Errorf("bundle: got %q, want %q", got, want)
	}
	if got, want := cfg.OrchestratorConfig, "minimal_config.yml"; got != want {
		t.Errorf("orchestrator config: got %q, want %q", got, want)
	}
	if got, want := cfg.LaunchAgentConfig, "launch_agent.yml"; got != want {
		t.Errorf("launch agent config: got %q, want %q", got, want)
	}
	if got, want := cfg.Info.CFBundleVersion, "9.9"; got != want {
		t.Errorf("CFBundleVersion: got %q, want %q", got, want)
	}

	// A file that is named and not there is an error, as is there being none.
	if _, _, err := loadBundleConfig(t.Context(), bundleFlag(t, filepath.Join(t.TempDir(), "absent.yaml"))); err == nil {
		t.Error("a missing bundle configuration was accepted")
	}
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if _, _, err := loadBundleConfig(t.Context(), bundleFlag(t)); err == nil {
		t.Error("no bundle configuration was accepted")
	}
	// Unknown fields are rejected.
	if err := os.WriteFile(path, []byte("bundel: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadBundleConfig(t.Context(), bundleFlag(t, path)); err == nil {
		t.Error("an unknown field was accepted")
	}
}

// TestShippedBundleConfig verifies that the bundle-builder.yaml in the tree
// still loads and yields a valid Info.plist, so that a bundle build fails at
// review rather than at release time.
func TestShippedBundleConfig(t *testing.T) {
	if _, err := os.Stat("bundle-builder.yaml"); err != nil {
		t.Skipf("no bundle-builder.yaml in the tree: %v", err)
	}
	cfg, _, err := loadBundleConfig(t.Context(), bundleFlag(t, "bundle-builder.yaml"))
	if err != nil {
		t.Fatalf("loadBundleConfig: %v", err)
	}
	version := buildtools.Version{Short: cfg.Info.CFBundleShortVersionString, Build: cfg.Info.CFBundleShortVersionString + "+abcdef12", Commit: "abcdef12", BuildTime: time.Now()}
	if _, err := buildInfoPlist(cfg.Info, defaultExecutable, orchestratorBundleID, version); err != nil {
		t.Errorf("the shipped bundle-builder.yaml yields an invalid Info.plist: %v", err)
	}
}

// TestDetailErr covers the error decoration applied to GitHub API failures,
// which is what turns a bare 404 into something actionable.
func TestDetailErr(t *testing.T) {
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "api.github.com", Path: "/repos/o/r"}}

	if got := detailErr(nil, req, nil); got != nil {
		t.Errorf("a nil error was decorated: %v", got)
	}

	base := os.ErrNotExist
	got := detailErr([]byte("not found\nsecond line\n"), req, base)
	if got == nil {
		t.Fatal("got nil, want an error")
	}
	msg := got.Error()
	for _, want := range []string{"api.github.com/repos/o/r", "not found"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
	// The cause is preserved for errors.Is.
	if !strings.Contains(msg, base.Error()) {
		t.Errorf("error %q does not wrap the cause", msg)
	}

	// A nil request, or one with no URL, is tolerated.
	if got := detailErr([]byte("body"), nil, base); got == nil || !strings.Contains(got.Error(), "body") {
		t.Errorf("nil request: got %v, want the body included", got)
	}
	// An empty body yields the location only.
	got = detailErr(nil, req, base)
	if got == nil || !strings.Contains(got.Error(), "api.github.com") {
		t.Errorf("empty body: got %v, want the location included", got)
	}
	// Neither body nor request leaves the error unchanged.
	if got := detailErr(nil, nil, base); got != base {
		t.Errorf("got %v, want the original error unchanged", got)
	}
}

func TestBundleConfigPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle-builder.yaml")
	content := `permissions:
  executable: "0755"
  macos_dir: "0700"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := loadBundleConfig(t.Context(), bundleFlag(t, path))
	if err != nil {
		t.Fatalf("loadBundleConfig: %v", err)
	}
	if got, want := cfg.Permissions.ExecutableMode(), fs.FileMode(0755); got != want {
		t.Errorf("ExecutableMode: got %04o, want %04o", got, want)
	}
	if got, want := cfg.Permissions.MacOSDirMode(), fs.FileMode(0700); got != want {
		t.Errorf("MacOSDirMode: got %04o, want %04o", got, want)
	}
}

func TestBundleInfoLSMinimumSystemVersion(t *testing.T) {
	version := buildtools.Version{Short: "1.0.0", Build: "1.0.0+abcdef12", Commit: "abcdef12345", BuildTime: time.Now()}
	info, err := buildInfoPlist(buildtools.InfoPlist{LSMinimumSystemVersion: "15.2"}, defaultExecutable, orchestratorBundleID, version)
	if err != nil {
		t.Fatalf("buildInfoPlist: %v", err)
	}
	if got, want := info.LSMinimumSystemVersion, "15.2"; got != want {
		t.Errorf("LSMinimumSystemVersion: got %q, want %q", got, want)
	}

	buildEnv := buildtools.GoBuildEnvForMacOSVersion(info.LSMinimumSystemVersion)
	wantEnv := []string{
		"MACOSX_DEPLOYMENT_TARGET=15.2",
		"CGO_CFLAGS=-mmacosx-version-min=15.2",
		"CGO_CXXFLAGS=-mmacosx-version-min=15.2",
		"CGO_LDFLAGS=-mmacosx-version-min=15.2",
	}
	if !slices.Equal(buildEnv, wantEnv) {
		t.Errorf("GoBuildEnvForMacOSVersion: got %v, want %v", buildEnv, wantEnv)
	}
}

func TestBuildAppBundleDryRun(t *testing.T) {
	tempDir := t.TempDir()
	cfg := BundleConfig{
		Bundle:             buildtools.Bundle{Path: filepath.Join(tempDir, "test.app")},
		OrchestratorConfig: "minimal_config.yml",
		LaunchAgentConfig:  "launch_agent.yml",
	}
	version := buildtools.Version{Short: "1.0.0", Build: "1.0.0", Commit: "abcdef12", BuildTime: time.Now()}
	info, err := buildInfoPlist(buildtools.InfoPlist{}, defaultExecutable, orchestratorBundleID, version)
	if err != nil {
		t.Fatalf("buildInfoPlist: %v", err)
	}
	err = buildAppBundle(context.Background(), cfg, info, "orchestrator", false, false, true, false)
	if err != nil {
		t.Fatalf("buildAppBundle dry run failed: %v", err)
	}
}

// TestBundleVersionFromInfoPlist checks that the release version is the one in
// the Info.plist fields, and that the build version is derived from it.
func TestBundleVersionFromInfoPlist(t *testing.T) {
	user := buildtools.InfoPlist{CFBundleShortVersionString: "2.0.0"}
	version, err := gitVersion(t.Context(), ".", user.CFBundleShortVersionString)
	if err != nil {
		t.Skipf("not in a git repository: %v", err)
	}
	info, err := buildInfoPlist(user, defaultExecutable, orchestratorBundleID, version)
	if err != nil {
		t.Fatal(err)
	}
	if info.CFBundleShortVersionString != "2.0.0" || !strings.HasPrefix(info.CFBundleVersion, "2.0.0+") {
		t.Errorf("got %q, %q", info.CFBundleShortVersionString, info.CFBundleVersion)
	}

	// Without one the default is used.
	version, err = gitVersion(t.Context(), ".", "")
	if err != nil {
		t.Skipf("not in a git repository: %v", err)
	}
	if info, err = buildInfoPlist(buildtools.InfoPlist{}, defaultExecutable, orchestratorBundleID, version); err != nil ||
		info.CFBundleShortVersionString != DefaultVersion {
		t.Errorf("got %q, %v, want %q", info.CFBundleShortVersionString, err, DefaultVersion)
	}
}

// TestBuildAppBundleDryRunIcon checks that the icon of the bundle configuration
// is created when there is one.
func TestBuildAppBundleDryRunIcon(t *testing.T) {
	tempDir := t.TempDir()
	icon := filepath.Join(tempDir, "icon.png")
	if err := os.WriteFile(icon, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := BundleConfig{
		Bundle:             buildtools.Bundle{Path: filepath.Join(tempDir, "test.app"), BundleSpec: buildtools.BundleSpec{Icon: icon}},
		OrchestratorConfig: "minimal_config.yml",
		LaunchAgentConfig:  "launch_agent.yml",
	}
	version := buildtools.Version{Short: "1.0.0", Build: "1.0.0", Commit: "abcdef12", BuildTime: time.Now()}
	info, err := buildInfoPlist(buildtools.InfoPlist{CFBundleIconFile: "AppIcon.icns"}, defaultExecutable, orchestratorBundleID, version)
	if err != nil {
		t.Fatal(err)
	}
	if err := buildAppBundle(context.Background(), cfg, info, "orchestrator", false, false, true, false); err != nil {
		t.Fatalf("buildAppBundle dry run failed: %v", err)
	}
}
