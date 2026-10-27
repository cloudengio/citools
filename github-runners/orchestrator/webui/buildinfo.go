// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package webui

import (
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"cloudeng.io/cmdutil"
)

// CurrentBuildInfo extracts build and version information for the running
// binary, along with the name of the host it is running on: the UI is usually
// reached over a forwarded port, so the host is not otherwise apparent from
// the browser.
func CurrentBuildInfo() BuildInfo {
	info := BuildInfo{
		GoVersion: runtime.Version(),
		Os:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
	// A host with no name is reported as having none rather than as an error:
	// it is a label for the UI, not something anything depends on.
	if host, err := os.Hostname(); err == nil && host != "" {
		info.Hostname = &host
	}
	goVersion, revision, lastCommit, buildTime, dirty, ok := cmdutil.VCSInfo()
	if goVersion != "" {
		info.GoVersion = goVersion
	}
	if ok {
		if revision != "" {
			info.Revision = &revision
			short := revision
			if len(short) > 8 {
				short = short[:8]
			}
			info.RevisionShort = &short
		}
		if !lastCommit.IsZero() {
			t := lastCommit.UTC().Format(time.RFC3339)
			info.RevisionTime = &t
		}
		info.Modified = &dirty
	}
	if !buildTime.IsZero() {
		t := buildTime.UTC().Format(time.RFC3339)
		info.BuildTime = &t
	}

	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Path != "" {
			info.Path = &bi.Path
		}
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = &bi.Main.Version
		}
	}
	return info
}
