// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"time"

	"cloudeng.io/cmdutil"
	"cloudeng.io/macos/buildtools"
)

// DefaultVersion is used when info_plist in bundle-builder.yaml does not set
// CFBundleShortVersionString.
const DefaultVersion = "0.0.0"

// gitVersion derives the version for a bundle built from the repository at
// dir, see buildtools.Git.Version, whose release version is short, or
// DefaultVersion if it is empty. A tree with uncommitted changes yields
// Dirty, which the caller must decide how to treat: a bundle stamped with a
// commit it does not match is worse than no stamp at all, especially once it
// is signed and notarized.
func gitVersion(ctx context.Context, dir, short string) (buildtools.Version, error) {
	if short == "" {
		short = DefaultVersion
	}
	return buildtools.NewGit(dir).Version(ctx, buildtools.NewCommandRunner(), short)
}

// VersionCommand reports the version of this binary.
type VersionCommand struct{}

// Run prints the build information recorded in the binary by the go tool. It
// comes from -buildvcs, which is on by default when building from a repository,
// so no linker flags are needed to stamp it.
func (VersionCommand) Run(_ context.Context, _ any, _ []string) error {
	goVersion, revision, lastCommit, buildTime, dirty, ok := cmdutil.VCSInfo()
	if !ok {
		fmt.Printf("%s\nno version information: built without VCS stamping\n", goVersion)
		return nil
	}
	suffix := ""
	if dirty {
		suffix = " (dirty)"
	}
	fmt.Printf("commit:  %s%s\n", revision, suffix)
	if dirty {
		if !buildTime.IsZero() {
			fmt.Printf("date:    %s\n", buildTime.UTC().Format(time.RFC3339))
		}
	} else {
		if !lastCommit.IsZero() {
			fmt.Printf("date:    %s\n", lastCommit.UTC().Format(time.RFC3339))
		}
	}
	fmt.Printf("go:      %s\n", goVersion)
	return nil
}
