// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package ui

import (
	"context"
)

// Mode determines whether the application presents as a regular user-facing
// application (with a Dock tile) or as an accessory/agent (menu bar only).
type Mode int

const (
	// ModeAccessory runs the UI as an accessory/background agent:
	// it creates an NSStatusItem in the macOS menu bar but shows NO Dock icon.
	ModeAccessory Mode = iota

	// ModeRegular runs the UI as a standard foreground application:
	// it shows a Dock icon and can also display a menu bar item or dialogs.
	ModeRegular
)

// Handler receives user interactions from the UI (menu items, dock quit, etc.).
type Handler interface {
	OnOpenWebUI()
	OnViewLogs()
	OnInstallService()
	OnRestartService()
	OnUninstallService()
	OnQuit()
	IsServiceInstalled() bool
}

// Dialogs provides native alert presentation.
type Dialogs interface {
	Confirm(title, message string) bool
	Notify(title, message string)
	ShowLog(title, message string, logSnippet []byte, logPath string)
}

// UI manages the platform UI lifecycle and dialog presentation.
type UI interface {
	Dialogs
	Start(ctx context.Context, handler Handler, webURL, logPath string) error
	Stop()
	IsAvailable() bool
}
