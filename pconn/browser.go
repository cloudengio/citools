// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"os/exec"
	"runtime"
)

// openBrowserFn opens the page served by 'pconn serve', and is replaced by the
// tests so that running them opens nothing.
var openBrowserFn = openBrowser

// openBrowser opens targetURL in the browser, using the command that each
// platform provides for it. It does not wait for the browser, which outlives
// the pconn that started it, but does reap the command so that one which exits
// at once leaves nothing behind.
func openBrowser(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
