// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"runtime"

	"cloudeng.io/cmdutil/subcmd"
)

const spec = `name: chrome-for-testing
summary: manage Chrome for Testing installations
commands:
  - name: get-manifest
    summary: retrieve Chrome for Testing version and download info
  - name: install
    summary: install a version of Chrome for Testing
`

func cli() *subcmd.CommandSetYAML {
	cmd := subcmd.MustFromYAML(spec)
	downloadInstallCmd := &downloadInstallCmd{}
	cmd.Set("get-manifest").MustRunner((&endpointsCmd{}).Get, &endpointsFlags{})
	cmd.Set("install").MustRunner(downloadInstallCmd.installCmd, &installFlags{})
	return cmd
}

func main() {
	subcmd.Dispatch(context.Background(), cli())
}

func defaultPlatform() string {
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
