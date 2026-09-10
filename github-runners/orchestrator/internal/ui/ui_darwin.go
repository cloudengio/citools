// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build darwin

package ui

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics
#include <stdlib.h>

int checkGUIAvailable(void);
void initAndRunCocoaApp(int mode, int serviceInstalled);
void stopCocoaApp(void);
int showNativeConfirm(const char *title, const char *message);
void showNativeNotify(const char *title, const char *message);
void showNativeLogDialog(const char *title, const char *message, const char *logSnippet, const char *logPath);
*/
import "C"

import (
	"context"
	"sync"
	"unsafe"
)

type darwinUI struct {
	mode Mode

	mu      sync.Mutex
	handler Handler
	running bool
}

var (
	activeUIMu sync.Mutex
	activeUI   *darwinUI
)

func New(mode Mode) UI {
	return &darwinUI{mode: mode}
}

func (u *darwinUI) IsAvailable() bool {
	return C.checkGUIAvailable() != 0
}

func (u *darwinUI) Start(ctx context.Context, handler Handler, webURL, logPath string) error {
	u.mu.Lock()
	u.handler = handler
	u.running = true
	u.mu.Unlock()

	activeUIMu.Lock()
	activeUI = u
	activeUIMu.Unlock()

	stopCh := make(chan struct{})
	defer close(stopCh)
	go func() {
		select {
		case <-ctx.Done():
			u.Stop()
		case <-stopCh:
		}
	}()

	defer func() {
		activeUIMu.Lock()
		if activeUI == u {
			activeUI = nil
		}
		activeUIMu.Unlock()

		u.mu.Lock()
		u.running = false
		u.mu.Unlock()
	}()

	installed := 0
	if handler != nil && handler.IsServiceInstalled() {
		installed = 1
	}

	modeVal := 0
	if u.mode == ModeRegular {
		modeVal = 1
	}

	C.initAndRunCocoaApp(C.int(modeVal), C.int(installed))
	return nil
}

func (u *darwinUI) Stop() {
	u.mu.Lock()
	running := u.running
	u.mu.Unlock()

	if running {
		C.stopCocoaApp()
	}
}

func (u *darwinUI) Confirm(title, message string) bool {
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	cMsg := C.CString(message)
	defer C.free(unsafe.Pointer(cMsg))
	return C.showNativeConfirm(cTitle, cMsg) != 0
}

func (u *darwinUI) Notify(title, message string) {
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	cMsg := C.CString(message)
	defer C.free(unsafe.Pointer(cMsg))
	C.showNativeNotify(cTitle, cMsg)
}

func (u *darwinUI) ShowLog(title, message string, logSnippet []byte, logPath string) {
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	cMsg := C.CString(message)
	defer C.free(unsafe.Pointer(cMsg))
	cSnippet := C.CString(string(logSnippet))
	defer C.free(unsafe.Pointer(cSnippet))
	cLogPath := C.CString(logPath)
	defer C.free(unsafe.Pointer(cLogPath))
	C.showNativeLogDialog(cTitle, cMsg, cSnippet, cLogPath)
}

//export goUIOpenWebUI
func goUIOpenWebUI() {
	activeUIMu.Lock()
	u := activeUI
	activeUIMu.Unlock()
	if u != nil && u.handler != nil {
		u.handler.OnOpenWebUI()
	}
}

//export goUIViewLogs
func goUIViewLogs() {
	activeUIMu.Lock()
	u := activeUI
	activeUIMu.Unlock()
	if u != nil && u.handler != nil {
		u.handler.OnViewLogs()
	}
}

//export goUIInstallService
func goUIInstallService() {
	activeUIMu.Lock()
	u := activeUI
	activeUIMu.Unlock()
	if u != nil && u.handler != nil {
		u.handler.OnInstallService()
	}
}

//export goUIRestartService
func goUIRestartService() {
	activeUIMu.Lock()
	u := activeUI
	activeUIMu.Unlock()
	if u != nil && u.handler != nil {
		u.handler.OnRestartService()
	}
}

//export goUIUninstallService
func goUIUninstallService() {
	activeUIMu.Lock()
	u := activeUI
	activeUIMu.Unlock()
	if u != nil && u.handler != nil {
		u.handler.OnUninstallService()
	}
}

//export goUIQuit
func goUIQuit() {
	activeUIMu.Lock()
	u := activeUI
	activeUIMu.Unlock()
	if u != nil {
		if u.handler != nil {
			u.handler.OnQuit()
		}
		u.Stop()
	}
}

//export goUIIsInstalled
func goUIIsInstalled() C.int {
	activeUIMu.Lock()
	u := activeUI
	activeUIMu.Unlock()
	if u != nil && u.handler != nil && u.handler.IsServiceInstalled() {
		return 1
	}
	return 0
}
