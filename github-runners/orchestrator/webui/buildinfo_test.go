// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package webui

import (
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
	t.Logf("BuildInfo: %+v", info)
}
