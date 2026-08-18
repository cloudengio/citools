// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
)

type installServiceFlags struct {
}

type installServiceCmd struct {
}

func (s *installServiceCmd) install(ctx context.Context, f any, args []string) error {
	return fmt.Errorf("installing as a service on macos is not yet supported")
}

/*
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.cloudeng.orchestrator</string>

    <key>ProgramArguments</key>
    <array>
        <string>/Users/admin/runner-orchestrator/orchestrator</string>
    </array>

    <key>UserName</key>
    <string>admin</string>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <true/>

    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
        <key>HOME</key>
        <string>/Users/admin</string>
    </dict>

    <key>StandardOutPath</key>
    <string>/Users/admin/runner-orchestrator/orchestrator.log</string>
    <key>StandardErrorPath</key>
    <string>/Users/admin/runner-orchestrator/orchestrator.err</string>
</dict>
</plist>
*/
