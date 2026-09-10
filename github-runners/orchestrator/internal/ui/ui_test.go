// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package ui_test

import (
	"testing"

	"github.com/cloudengio/citools/runners/macos/orchestrator/internal/ui"
)

func TestUINew(t *testing.T) {
	uAccessory := ui.New(ui.ModeAccessory)
	if uAccessory == nil {
		t.Fatalf("ui.New(ModeAccessory) returned nil")
	}

	uRegular := ui.New(ui.ModeRegular)
	if uRegular == nil {
		t.Fatalf("ui.New(ModeRegular) returned nil")
	}

	_ = uAccessory.IsAvailable()
	_ = uRegular.IsAvailable()
}
