// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build darwin

package main

import (
	"bytes"
	"context"
	"os/exec"
)

type Tart struct{}

func (t *Tart) Clone(ctx context.Context, vm VMConfig) error {
	return exec.CommandContext(ctx, "tart", "clone", vm.Image, vm.Name).Run()
}

func (t *Tart) Start(ctx context.Context, vm VMConfig) error {
	return exec.CommandContext(ctx, "tart", "run", "--background", vm.Name).Run()
}

func (t *Tart) RunInfo(ctx context.Context, vm VMConfig) (VMInfo, error) {
	out, err := exec.CommandContext(ctx, "tart", "ip", vm.Name).Output()
	if err != nil {
		return VMInfo{}, err
	}
	return VMInfo{Address: string(bytes.TrimSpace(out))}, nil
}

func (t *Tart) Stop(ctx context.Context, vm VMConfig) error {
	return exec.CommandContext(ctx, "tart", "stop", vm.Name).Run()
}

func (t *Tart) Delete(ctx context.Context, vm VMConfig) error {
	return exec.CommandContext(ctx, "tart", "delete", vm.Name).Run()
}
