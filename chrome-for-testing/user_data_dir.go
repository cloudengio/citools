// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type userDataDirFlags struct {
	OS string `subcmd:"os,,operating system for which to determine the user data dir (linux, darwin, windows). If not specified, the current OS is used."`
}

func (d *downloadInstallCmd) userDataDirCmd(ctx context.Context, f any, args []string) error {
	fv := f.(*userDataDirFlags)
	goos := fv.OS
	if goos == "" {
		goos = runtime.GOOS
	}
	udDir, err := getUserDataDir(goos)
	if err != nil {
		return fmt.Errorf("determining user data dir: %w", err)
	}
	fmt.Println(udDir)
	return nil

}

func getUserDataDir(goos string) (string, error) {
	switch goos {
	case "linux":
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("HOME environment variable not set")
		}
		return filepath.Join(home,
			"Library", "Application Support", "Google", "Chrome for Testing"), nil
	case "darwin":
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("HOME environment variable not set")
		}
		return filepath.Join(home, ".config", "google-chrome-for-testing"), nil
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			return "", fmt.Errorf("LOCALAPPDATA environment variable not set")
		}
		return filepath.Join(localAppData, "Google", "Chrome for Testing", "User Data", "Default"), nil
	default:
		return "", fmt.Errorf("unsupported platform %q", goos)
	}
}
