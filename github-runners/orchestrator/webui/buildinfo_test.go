// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package webui

import (
	"os"
	"testing"
)

func TestCurrentBuildInfo(t *testing.T) {
	info := CurrentBuildInfo()
	if info.GoVersion == "" {
		t.Error("expected non-empty GoVersion")
	}
	if info.Os == "" {
		t.Error("expected non-empty Os")
	}
	if info.Arch == "" {
		t.Error("expected non-empty Arch")
	}
	if info.BuildTime == nil || *info.BuildTime == "" {
		t.Error("expected non-nil BuildTime from executable")
	}
	// The host name identifies which orchestrator the UI is showing, so it is
	// reported whenever the OS will supply one.
	host, err := os.Hostname()
	if err != nil {
		t.Skipf("no host name available: %v", err)
	}
	if info.Hostname == nil {
		t.Errorf("expected Hostname %q, got none", host)
	} else if got, want := *info.Hostname, host; got != want {
		t.Errorf("Hostname: got %v, want %v", got, want)
	}
	t.Logf("BuildInfo: %+v", info)
}
