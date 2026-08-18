// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"

	"cloudeng.io/cmdutil/subcmd"
)

const spec = `name: github self hosted runner orchestrator
summary: manage github self hosted runner orchestration
commands:
  - name: install-service
    summary: install as a service
  - name: run
    summary: run the orchestrator
`

func cli() *subcmd.CommandSetYAML {
	cmd := subcmd.MustFromYAML(spec)
	installServiceCmd := &installServiceCmd{}
	cmd.Set("install-service").MustRunner(installServiceCmd.install, &installServiceFlags{})
	return cmd
}

func main() {
	subcmd.Dispatch(context.Background(), cli())
}
