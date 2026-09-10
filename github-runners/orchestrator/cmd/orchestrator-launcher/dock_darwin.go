// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build darwin

package main

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#include <stdlib.h>

void runCocoaApp(void);
void stopCocoaApp(void);
int showNativeConfirm(const char *title, const char *message);
void showNativeNotify(const char *title, const char *message);
void showNativeLogDialog(const char *title, const char *message, const char *logSnippet, const char *logPath);
*/
import "C"

import (
	"runtime"
	"unsafe"
)

// runApp runs the launcher inside a Cocoa application. Running the event loop
// (rather than just calling finishLaunching) is what actually stops the Dock
// icon bouncing; the regular activation policy keeps the icon so the app can be
// quit from the Dock. The launcher's work runs on a background thread (see
// launcherMain) so it doesn't block the run loop.
func runApp() {
	runtime.LockOSThread() // AppKit must run on the main thread.
	C.runCocoaApp()
}

// launcherMain is invoked by the Cocoa delegate once the app has finished
// launching. It runs the launcher's work and then terminates the app.
//
//export launcherMain
func launcherMain() {
	runLauncher()
	C.stopCocoaApp()
}

// launcherWillTerminate is invoked by the Cocoa delegate when the app is about
// to quit (e.g. the user chose Quit from the Dock). It shuts the orchestrator
// child down, since quitting a Cocoa app does not deliver a Unix signal.
//
//export launcherWillTerminate
func launcherWillTerminate() {
	terminateChild()
}

func confirm(message string) bool {
	cTitle := C.CString(dialogTitle)
	defer C.free(unsafe.Pointer(cTitle))
	cMsg := C.CString(message)
	defer C.free(unsafe.Pointer(cMsg))
	return C.showNativeConfirm(cTitle, cMsg) != 0
}

func notify(message string) {
	cTitle := C.CString(dialogTitle)
	defer C.free(unsafe.Pointer(cTitle))
	cMsg := C.CString(message)
	defer C.free(unsafe.Pointer(cMsg))
	C.showNativeNotify(cTitle, cMsg)
}

func showLogDialog(header string, logSnippet []byte, fullLogPath string) {
	cTitle := C.CString(dialogTitle)
	defer C.free(unsafe.Pointer(cTitle))
	cHeader := C.CString(header)
	defer C.free(unsafe.Pointer(cHeader))
	cSnippet := C.CString(string(logSnippet))
	defer C.free(unsafe.Pointer(cSnippet))
	cLogPath := C.CString(fullLogPath)
	defer C.free(unsafe.Pointer(cLogPath))
	C.showNativeLogDialog(cTitle, cHeader, cSnippet, cLogPath)
}
