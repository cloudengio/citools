// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloudeng.io/cmdutil"
	"cloudeng.io/macos/buildtools"
)

// DefaultVersion is used when installer.yaml does not set one.
const DefaultVersion = "0.0.0"

// versionInfo describes the build stamped into the bundles. The outer app and
// the nested one are built from a single tree, so both carry the same values:
// a difference between them would be a bug rather than information.
type versionInfo struct {
	// Short is CFBundleShortVersionString, the human-readable release version.
	Short string
	// Build is CFBundleVersion, the release version with the commit appended.
	Build string
	// Commit is the full hash, and BuildTime when the bundle was built.
	Commit    string
	BuildTime time.Time
	// Dirty reports uncommitted changes in the tree the bundle was built from.
	Dirty bool
}

// keys returns the Info.plist keys recording the version, for merging into the
// bundle's other keys. CGCommit and CGBuildTime have no CFBundle equivalent;
// they record which tree the bundle came from and when, which the version
// strings alone cannot.
func (v versionInfo) keys() map[string]any {
	return map[string]any{
		"CFBundleShortVersionString": v.Short,
		"CFBundleVersion":            v.Build,
		"CGCommit":                   v.Commit,
		"CGBuildTime":                v.BuildTime.UTC().Format(time.RFC3339),
	}
}

// gitVersion derives the version for a bundle built from the repository at dir.
// A tree with uncommitted changes yields Dirty, which the caller must decide
// how to treat: a bundle stamped with a commit it does not match is worse than
// no stamp at all, especially once it is signed and notarized.
func gitVersion(ctx context.Context, dir, short string) (versionInfo, error) {
	if short == "" {
		short = DefaultVersion
	}
	v := versionInfo{Short: short, Build: short, BuildTime: time.Now()}

	git := buildtools.NewGit(dir)
	runner := buildtools.NewCommandRunner()

	full, err := git.Hash(ctx, runner, "HEAD", 40)
	if err != nil {
		return v, fmt.Errorf("determining the git revision of %v: %w", dir, err)
	}
	v.Commit = strings.TrimSpace(full.Output())

	abbrev, err := git.Hash(ctx, runner, "HEAD", 8)
	if err != nil {
		return v, fmt.Errorf("determining the short git revision of %v: %w", dir, err)
	}
	v.Build = short + "+" + strings.TrimSpace(abbrev.Output())

	status, err := gitStatus(ctx, runner, dir)
	if err != nil {
		return v, err
	}
	if v.Dirty = status != ""; v.Dirty {
		v.Build += "-dirty"
	}
	return v, nil
}

// gitStatus returns the porcelain status of the tree at dir, empty when clean.
func gitStatus(ctx context.Context, runner *buildtools.CommandRunner, dir string) (string, error) {
	res, err := runner.Run(buildtools.ContextWithCWD(ctx, dir), "git", "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("determining whether %v is clean: %w", dir, err)
	}
	return strings.TrimSpace(res.Output()), nil
}

// VersionCommand reports the version of this binary.
type VersionCommand struct{}

// Run prints the build information recorded in the binary by the go tool. It
// comes from -buildvcs, which is on by default when building from a repository,
// so no linker flags are needed to stamp it.
func (VersionCommand) Run(_ context.Context, _ any, _ []string) error {
	goVersion, revision, lastCommit, dirty, ok := cmdutil.VCSInfo()
	if !ok {
		fmt.Printf("%s\nno version information: built without VCS stamping\n", goVersion)
		return nil
	}
	suffix := ""
	if dirty {
		suffix = " (dirty)"
	}
	fmt.Printf("commit:  %s%s\n", revision, suffix)
	fmt.Printf("date:    %s\n", lastCommit.UTC().Format(time.RFC3339))
	fmt.Printf("go:      %s\n", goVersion)
	return nil
}
