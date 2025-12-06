// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type Parallels struct{}

func (p *Parallels) Clone(ctx context.Context, vm VMConfig) error {
	return exec.CommandContext(ctx, "prlctl", "clone", vm.Image, "--name", vm.Name).Run()
}

func (p *Parallels) Start(ctx context.Context, vm VMConfig) error {
	return exec.CommandContext(ctx, "prlctl", "start", vm.Name).Run()
}

type prlInfo struct {
	IPv4Configured string `json:"ip_configured"`
}

func (p *Parallels) RunInfo(ctx context.Context, vm VMConfig) (VMInfo, error) {
	out, err := exec.CommandContext(ctx, "prlctl", "list", vm.Name, "--json", "-f").Output()
	if err != nil {
		return VMInfo{}, err
	}
	var infos []prlInfo
	if err := json.Unmarshal(out, &infos); err != nil {
		return VMInfo{}, err
	}
	if len(infos) == 0 {
		return VMInfo{}, fmt.Errorf("no info found for vm: %s", vm.Name)
	}
	return VMInfo{Address: infos[0].IPv4Configured}, nil
}

func (p *Parallels) Stop(ctx context.Context, vm VMConfig) error {
	return exec.CommandContext(ctx, "prlctl", "stop", vm.Name).Run()
}

func (p *Parallels) Delete(ctx context.Context, vm VMConfig) error {
	return exec.CommandContext(ctx, "prlctl", "delete", vm.Name).Run()
}
