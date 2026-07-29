// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"

	"cloudeng.io/cmdutil/subcmd"
)

const spec = `name: chrome-for-testing
summary: manage Chrome for Testing installations
commands:
  - name: get-manifest
    summary: retrieve Chrome for Testing version and download info
  - name: install
    summary: install a version of Chrome for Testing
  - name: paths
    summary: emit install paths (chrome-path, chrome-user-data-dir) for an already-installed Chrome for Testing without downloading
  - name: user-data-dir
    summary: determine the user data dir for Chrome for Testing based on OS
`

func cli() *subcmd.CommandSetYAML {
	cmd := subcmd.MustFromYAML(spec)
	downloadInstallCmd := &downloadInstallCmd{}
	cmd.Set("get-manifest").MustRunner((&endpointsCmd{}).Get, &endpointsFlags{})
	cmd.Set("install").MustRunner(downloadInstallCmd.installCmd, &installFlags{})
	cmd.Set("paths").MustRunner(downloadInstallCmd.pathsCmd, &pathsFlags{})
	cmd.Set("user-data-dir").MustRunner(downloadInstallCmd.userDataDirCmd, &userDataDirFlags{})
	return cmd
}

func getSetting(s []debug.BuildSetting, key string) string {
	for _, setting := range s {
		if setting.Key == key {
			return setting.Value
		}
	}
	return ""
}

func gitHashShort(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

func main() {
	if bi, ok := debug.ReadBuildInfo(); ok {
		fmt.Printf("%v: build info: %v %v\n",
			os.Args[0],
			gitHashShort(getSetting(bi.Settings, "vcs.revision")),
			getSetting(bi.Settings, "vcs.time"))
	}
	subcmd.Dispatch(context.Background(), cli())
}

func currentPlatform() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "mac-arm64", nil
		}
		return "mac-x64", nil
	case "linux":
		// Chrome for Testing only publishes an x64 (amd64) Linux build.
		if runtime.GOARCH != "amd64" {
			return "", fmt.Errorf("Chrome for Testing has no linux/%s build; use an x64 (amd64) runner", runtime.GOARCH)
		}
		return "linux64", nil
	case "windows":
		return "win64", nil
	default:
		return "", fmt.Errorf("unsupported operating system %q", runtime.GOOS)
	}
}
