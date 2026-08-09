// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/macos/tartvm"
	"cloudeng.io/vms/vmspool"
	"github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient"
)

// TartCommand implements the tart subcommands used to inspect and clean up the
// VMs created by the orchestrator's VM pools.
type TartCommand struct{}

type TartListVMsFlags struct {
	JSON bool `subcmd:"json,false,print the tart list entries as JSON rather than as a table"`
}

func (t TartCommand) ListVMs(ctx context.Context, flags any, _ []string) error {
	fv := flags.(*TartListVMsFlags)
	entries, err := listTartVMs(ctx)
	if err != nil {
		return err
	}
	if fv.JSON {
		if entries == nil {
			entries = tartvm.ListEntries{}
		}
		out, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}
	return writeTartTable(os.Stdout, entries)
}

type TartDeleteVMsFlags struct {
	DryRun      bool          `subcmd:"dry-run,false,list the VMs that would be deleted without deleting them"`
	StopTimeout time.Duration `subcmd:"stop-timeout,10s,time to wait for a running VM to shut down gracefully before tart forcibly stops it"`
}

func (t TartCommand) DeleteVMs(ctx context.Context, flags any, _ []string) error {
	fv := flags.(*TartDeleteVMsFlags)
	if fv.DryRun {
		entries, err := listTartVMs(ctx)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			fmt.Printf("would delete %s (%s)\n", entry.Name, entry.State)
		}
		return nil
	}
	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return fmt.Errorf("no config in context")
	}
	deleted, err := vmsclient.DeleteTartVMs(ctx, cfg.VMPools, fv.StopTimeout)
	// Report what was deleted even if some of the deletions failed.
	for _, name := range deleted {
		fmt.Printf("deleted %s\n", name)
	}
	return err
}

// sweepOrphanedVMs deletes the VMs left behind by a previous run of the
// orchestrator that exited without cleaning up after itself. No in-process
// bookkeeping survives a crash or a SIGKILL, so the VMs are identified by the
// naming convention used by the configured VM pools. Failures are logged
// rather than returned: an orphaned VM costs disk space but does not prevent
// the orchestrator from running.
func sweepOrphanedVMs(ctx context.Context, cfg Config) {
	deleted, err := vmsclient.DeleteTartVMs(ctx, cfg.VMPools, sweepStopTimeout(cfg))
	if len(deleted) > 0 {
		ctxlog.Info(ctx, "deleted VMs orphaned by a previous run", "vms", deleted)
	}
	if err != nil {
		ctxlog.Error(ctx, "failed to delete VMs orphaned by a previous run", "error", err)
	}
}

// sweepStopTimeout returns the longest stop timeout configured for any VM pool,
// so that an orphaned VM that is still running is given as much time to shut
// down as it would have been given by its own pool.
func sweepStopTimeout(cfg Config) time.Duration {
	timeout := vmspool.DefaultStopTimeout
	for _, poolCfg := range cfg.VMPools {
		if poolCfg.StopTimeout > timeout {
			timeout = poolCfg.StopTimeout
		}
	}
	return timeout
}

func listTartVMs(ctx context.Context) (tartvm.ListEntries, error) {
	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("no config in context")
	}
	return vmsclient.ListTartVMs(ctx, cfg.VMPools)
}

func writeTartTable(out io.Writer, entries tartvm.ListEntries) error {
	tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tRUNNING\tDISK\tSIZE\tACCESSED")
	for _, entry := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%v\t%d\t%d\t%s\n",
			entry.Name, entry.State, entry.Running, entry.Disk, entry.Size,
			entry.Accessed.Local().Format(time.RFC3339))
	}
	return tw.Flush()
}
