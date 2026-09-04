// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build darwin

package main

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics
int checkGUIAvailable(void);
void initAndRunCocoaApp(int serviceInstalled);
void stopCocoaApp(void);
*/
import "C"

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
)

var (
	menuBarMu      sync.Mutex
	menuBarCancel  context.CancelFunc
	menuBarWebURL  string
	menuBarLogPath string
	menuBarRunning bool
)

func isGUIAvailable() bool {
	return C.checkGUIAvailable() != 0
}

func defaultServiceLogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", serviceLabel, internal.OrchestratorBinary+".service.out.log")
}

//export goStatusItemOpenWebUI
func goStatusItemOpenWebUI() {
	menuBarMu.Lock()
	webURL := menuBarWebURL
	menuBarMu.Unlock()
	if webURL != "" {
		_ = exec.Command("open", webURL).Start()
	}
}

//export goStatusItemViewLogs
func goStatusItemViewLogs() {
	menuBarMu.Lock()
	lp := menuBarLogPath
	menuBarMu.Unlock()
	if lp == "" {
		lp = defaultServiceLogPath()
	}
	_ = exec.Command("open", lp).Start()
}

//export goStatusItemRestart
func goStatusItemRestart() {
	agent := serviceAgent()
	if agent.IsInstalled() {
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = runSteps(context.Background(), false, agent.Restart())
		}()
	}
}

//export goStatusItemUninstall
func goStatusItemUninstall() {
	agent := serviceAgent()
	if agent.IsInstalled() {
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = runSteps(context.Background(), false, agent.Uninstall()...)
			menuBarMu.Lock()
			cancel := menuBarCancel
			menuBarMu.Unlock()
			if cancel != nil {
				cancel()
			}
			stopStatusItem()
		}()
	}
}

//export goStatusItemQuit
func goStatusItemQuit() {
	menuBarMu.Lock()
	cancel := menuBarCancel
	menuBarMu.Unlock()
	if cancel != nil {
		cancel()
	}
	stopStatusItem()
}

func startStatusItem(ctx context.Context, cancel context.CancelFunc, webURL, logPath string) {
	menuBarMu.Lock()
	menuBarCancel = cancel
	menuBarWebURL = webURL
	menuBarLogPath = logPath
	menuBarRunning = true
	menuBarMu.Unlock()

	installed := 0
	if serviceAgent().IsInstalled() {
		installed = 1
	}
	C.initAndRunCocoaApp(C.int(installed))

	menuBarMu.Lock()
	menuBarRunning = false
	menuBarMu.Unlock()
}

func stopStatusItem() {
	menuBarMu.Lock()
	running := menuBarRunning
	menuBarMu.Unlock()
	if running {
		C.stopCocoaApp()
	}
}
