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
  - name: user-data-dir
    summary: determine the user data dir for Chrome for Testing based on OS
`

func cli() *subcmd.CommandSetYAML {
	cmd := subcmd.MustFromYAML(spec)
	downloadInstallCmd := &downloadInstallCmd{}
	cmd.Set("get-manifest").MustRunner((&endpointsCmd{}).Get, &endpointsFlags{})
	cmd.Set("install").MustRunner(downloadInstallCmd.installCmd, &installFlags{})
	cmd.Set("user-data-dir").MustRunner(downloadInstallCmd.userDataDirCmd, &installFlags{})
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

func currentPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "mac-arm64"
		}
		return "mac-x64"
	case "linux":
		return "linux64"
	case "windows":
		return "win64"
	default:
		return ""
	}
}
