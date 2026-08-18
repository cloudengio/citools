// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"cloudeng.io/cmdutil/registry"
)

var vmManagers = &registry.T[VM]{}

type Orchestrator struct {
	vm     VM
	config Config
}

func NewOrchestrator(ctx context.Context, config Config) (*Orchestrator, error) {
	vm, err := vmManagers.Get(config.VM.VMType)(ctx, config.VM)
	if err != nil {
		return nil, err
	}
	return &Orchestrator{
			vm:     vm,
			config: config},
		nil
}

func (o *Orchestrator) Run(ctx context.Context) error {
	for range o.config.VM.NumVMs {
		go o.vmloop(ctx)
	}
	fmt.Println("running orchestrator")
	return nil
}

func (o *Orchestrator) vmloop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("vmloop: %w", ctx.Err())
		default:
			if err := o.runVM(ctx); err != nil {
				return err
			}
		}
	}
}

func (o *Orchestrator) runVM(ctx context.Context) error {
	if err := o.vm.Clone(ctx, o.config.VM); err != nil {
		return err
	}
	if err := o.vm.Start(ctx, o.config.VM); err != nil {
		return err
	}
	vmi, err := o.vm.RunInfo(ctx, o.config.VM)
	if err != nil {
		return err
	}
	if err := o.waitForNetwork(ctx, vmi); err != nil {
		return err
	}
	return nil
}

func (o *Orchestrator) waitForNetwork(ctx context.Context, vm VMInfo) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for VM network: %w", ctx.Err())
		case <-ticker.C:
			// Check if Port 22 is open
			conn, err := net.DialTimeout("tcp", net.JoinHostPort(vm.Address, "22"), 1*time.Second)
			if err == nil {
				conn.Close()
				return nil
			}
		}
	}
}

func (o *Orchestrator) runRunner(ctx context.Context) error {
	// Note: We pass the JIT token as an argument to the internal script
	sshCmd := fmt.Sprintf("bash /Users/runner/actions-runner/run.sh")
	_ = sshCmd

	/*
		err = runCommand("ssh",
			"-o", "StrictHostKeyChecking=no",
			"-o", "ConnectTimeout=5",

			fmt.Sprintf("%s@%s", VMUser, ip),
			sshCmd,
		)

		if err != nil {
			return fmt.Errorf("runner execution failed or timed out: %w", err)
		}*/
}

// runCommand is a helper to run a shell command and stream output to stdout
func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
