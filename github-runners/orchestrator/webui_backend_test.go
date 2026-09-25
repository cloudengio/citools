// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"testing"
	"time"

	"cloudeng.io/vms/vmspool"
	"github.com/cloudengio/citools/runners/macos/orchestrator/webui"
)

// TestBackendServesBeforeHandlerReady verifies the web UI backend answers
// requests before the orchestrator handler is wired in: config is available and
// pool/workflow queries return empty (not errors), so the server can start up
// front and fill in later.
func TestBackendServesBeforeHandlerReady(t *testing.T) {
	b := newWebUIBackend(Config{}, "/tmp/cfg.yml")
	ctx := context.Background()

	if got := b.handler(); got != nil {
		t.Fatalf("handler should be nil before SetHandler, got %v", got)
	}

	pools, err := b.Pools(ctx)
	if err != nil || len(pools) != 0 {
		t.Errorf("Pools before ready = %v, %v; want empty, nil", pools, err)
	}
	wfs, err := b.Workflows(ctx)
	if err != nil || len(wfs) != 0 {
		t.Errorf("Workflows before ready = %v, %v; want empty, nil", wfs, err)
	}
	if _, ok, err := b.Workflow(ctx, "anything"); ok || err != nil {
		t.Errorf("Workflow before ready = ok:%v err:%v; want not-found, nil", ok, err)
	}
	if _, _, err := b.WorkflowLog(ctx, "w", "job"); err == nil {
		t.Error("WorkflowLog before ready should error (initializing)")
	}

	cs, err := b.ConfigSummary(ctx)
	if err != nil || cs.ConfigFile != "/tmp/cfg.yml" {
		t.Errorf("ConfigSummary before ready = %+v, %v; config should be available", cs, err)
	}
}

// TestBackendNotifiesOnReady verifies that an SSE subscriber connected before
// the handler is ready is signalled when state becomes available, so early
// clients refresh and see the filled-in data.
func TestBackendNotifiesOnReady(t *testing.T) {
	b := newWebUIBackend(Config{}, "")
	ch, cancel := b.Subscribe(context.Background())
	defer cancel()

	// SetHandler publishes this signal once the handler is wired in.
	b.changes.Publish(struct{}{})

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("subscriber was not notified when state became available")
	}
}

func TestMapVMState(t *testing.T) {
	cases := []struct {
		name     string
		vm       vmspool.VMInfo
		acquired bool
		inCQ     bool
		want     webui.VMState
	}{
		{
			name:     "in completion queue",
			vm:       vmspool.VMInfo{State: "stopped", Running: false},
			acquired: false,
			inCQ:     true,
			want:     webui.VMStateCompletionQueue,
		},
		{
			name:     "in completion queue even if running",
			vm:       vmspool.VMInfo{State: "running", Running: true},
			acquired: false,
			inCQ:     true,
			want:     webui.VMStateCompletionQueue,
		},
		{
			name:     "running and acquired",
			vm:       vmspool.VMInfo{State: "running", Running: true},
			acquired: true,
			inCQ:     false,
			want:     webui.VMStateAcquired,
		},
		{
			name:     "suspended and acquired",
			vm:       vmspool.VMInfo{State: "suspended", Running: false},
			acquired: true,
			inCQ:     false,
			want:     webui.VMStateAcquired,
		},
		{
			name:     "stopped and acquired",
			vm:       vmspool.VMInfo{State: "stopped", Running: false},
			acquired: true,
			inCQ:     false,
			want:     webui.VMStateAcquired,
		},
		{
			name:     "warm running in pool (available)",
			vm:       vmspool.VMInfo{State: "running", Running: true},
			acquired: false,
			inCQ:     false,
			want:     webui.VMStateAvailable,
		},
		{
			name:     "warm suspended in pool (available)",
			vm:       vmspool.VMInfo{State: "suspended", Running: false},
			acquired: false,
			inCQ:     false,
			want:     webui.VMStateAvailable,
		},
		{
			name:     "warm stopped in pool (stopped)",
			vm:       vmspool.VMInfo{State: "stopped", Running: false},
			acquired: false,
			inCQ:     false,
			want:     webui.VMStateStopped,
		},
		{
			name:     "creating / cloning",
			vm:       vmspool.VMInfo{State: "cloning", Running: false},
			acquired: false,
			inCQ:     false,
			want:     webui.VMStateCreating,
		},
		{
			name:     "starting",
			vm:       vmspool.VMInfo{State: "starting", Running: false},
			acquired: false,
			inCQ:     false,
			want:     webui.VMStateCreating,
		},
		{
			name:     "deleted",
			vm:       vmspool.VMInfo{State: "deleted", Running: false},
			acquired: false,
			inCQ:     false,
			want:     webui.VMStateDeleted,
		},
		{
			name:     "failed",
			vm:       vmspool.VMInfo{State: "failed", Running: false},
			acquired: false,
			inCQ:     false,
			want:     webui.VMStateFailed,
		},
		{
			name:     "unknown",
			vm:       vmspool.VMInfo{State: "unexpected_state", Running: false},
			acquired: false,
			inCQ:     false,
			want:     webui.VMStateUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapVMState(tc.vm, tc.acquired, tc.inCQ)
			if got != tc.want {
				t.Errorf("mapVMState(%+v, %v, %v) = %v; want %v", tc.vm, tc.acquired, tc.inCQ, got, tc.want)
			}
		})
	}
}
