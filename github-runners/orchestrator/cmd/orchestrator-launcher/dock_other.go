// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build !darwin

package main

import (
	"fmt"
	"os"
)

// runApp runs the launcher directly on non-macOS platforms.
func runApp() { runLauncher() }

func confirm(_ string) bool {
	return false
}

func notify(message string) {
	fmt.Fprintln(os.Stderr, message)
}

func showLogDialog(header string, logSnippet []byte, fullLogPath string) {
	fmt.Fprintf(os.Stderr, "%s\n\n%s\n\nFull log: %s\n", header, string(logSnippet), fullLogPath)
}
