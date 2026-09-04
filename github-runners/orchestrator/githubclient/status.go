// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubclient

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"cloudeng.io/sync/patterns"
)

// WorkflowState is the lifecycle state of a workflow job within the
// orchestrator. It intentionally mirrors the API's WorkflowState enum.
type WorkflowState string

const (
	WorkflowQueued    WorkflowState = "queued"
	WorkflowAcquiring WorkflowState = "acquiring"
	WorkflowRunning   WorkflowState = "running"
	// WorkflowVMCompleted means the job finished on the local VM (the runner
	// exited and the VM was stopped) but GitHub has not yet acknowledged
	// completion via a workflow_job "completed" webhook.
	WorkflowVMCompleted WorkflowState = "vm_completed"
	// WorkflowCompleted means GitHub has delivered the workflow_job "completed"
	// webhook, i.e. the run is finished end-to-end.
	WorkflowCompleted WorkflowState = "completed"
	WorkflowFailed    WorkflowState = "failed"
	WorkflowCanceled  WorkflowState = "canceled"
)

// WorkflowSnapshot is a point-in-time view of a single workflow job tracked by
// the orchestrator. It is a flat, serialization-friendly projection of a
// WorkflowInstance plus its lifecycle state and timestamps.
type WorkflowSnapshot struct {
	Name         string
	State        WorkflowState
	RepoFullName string
	RepoURL      string
	WorkflowName string
	JobName      string
	JobID        int64
	RunID        int64
	JobURL       string
	Labels       []string
	Pool         string
	VMID         string
	Result       string
	Err          string
	QueuedAt     time.Time
	StartedAt    time.Time
	// VMCompletedAt is when the job finished on the local VM (state
	// vm_completed); CompletedAt is when GitHub acknowledged completion.
	VMCompletedAt time.Time
	CompletedAt   time.Time
	JobLogPath    string
	DiagLogPath   string
	JobStarted    *JobStartedInfo
}

// statusTracker records the lifecycle of every workflow job the orchestrator
// handles, retaining completed jobs for a configurable period so the web UI can
// still show recently-finished work after the underlying instance is gone. It
// is safe for concurrent use and notifies subscribers on every change.
type statusTracker struct {
	mu        sync.Mutex
	records   map[string]*WorkflowSnapshot
	retention time.Duration
	pubsub    *patterns.PubSub[struct{}]
}

func newStatusTracker(retention time.Duration) *statusTracker {
	return &statusTracker{
		records:   make(map[string]*WorkflowSnapshot),
		retention: retention,
		pubsub:    patterns.New[struct{}](),
	}
}

// subscribe returns a change-signal channel and a cancel function; the
// subscription is also released when ctx is cancelled.
func (t *statusTracker) subscribe(ctx context.Context) (<-chan struct{}, func()) {
	sub := t.pubsub.Subscribe(ctx, 1)
	return sub.C(), func() { t.pubsub.Unsubscribe(sub) }
}

// upsert applies mutate to the snapshot for name (creating it if absent) and
// notifies subscribers. The caller-supplied closure runs under the lock.
func (t *statusTracker) upsert(name string, mutate func(*WorkflowSnapshot)) {
	t.mu.Lock()
	rec := t.records[name]
	if rec == nil {
		rec = &WorkflowSnapshot{Name: name}
		t.records[name] = rec
	}
	mutate(rec)
	t.pruneLocked()
	t.mu.Unlock()
	t.pubsub.Publish(struct{}{})
}

// pruneLocked drops completed records older than the retention period. The
// caller must hold t.mu.
func (t *statusTracker) pruneLocked() {
	if t.retention <= 0 {
		return
	}
	cutoff := time.Now().Add(-t.retention)
	for name, rec := range t.records {
		switch {
		case isTerminal(rec.State) && !rec.CompletedAt.IsZero() && rec.CompletedAt.Before(cutoff):
			delete(t.records, name)
		case rec.State == WorkflowVMCompleted && !rec.VMCompletedAt.IsZero() && rec.VMCompletedAt.Before(cutoff):
			// GitHub never acknowledged completion within the retention window;
			// drop the record so vm_completed entries don't accumulate forever.
			delete(t.records, name)
		}
	}
}

func isTerminal(s WorkflowState) bool {
	switch s {
	case WorkflowCompleted, WorkflowFailed, WorkflowCanceled:
		return true
	}
	return false
}

func (t *statusTracker) list() []WorkflowSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]WorkflowSnapshot, 0, len(t.records))
	for _, rec := range t.records {
		out = append(out, *rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].QueuedAt.Equal(out[j].QueuedAt) {
			return out[i].QueuedAt.After(out[j].QueuedAt)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (t *statusTracker) get(name string) (WorkflowSnapshot, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, ok := t.records[name]
	if !ok {
		return WorkflowSnapshot{}, false
	}
	return *rec, true
}

// snapshotFromInstance projects the immutable identity fields of a
// WorkflowInstance into a snapshot mutation.
func snapshotFromInstance(wi *WorkflowInstance) func(*WorkflowSnapshot) {
	return func(rec *WorkflowSnapshot) {
		rec.Pool = wi.RunnerConfig.VMPoolName
		rec.RepoURL = wi.RepoURL
		rec.JobLogPath = wi.LogName
		rec.DiagLogPath = wi.DiagName
		if wi.JobStarted != nil {
			rec.JobStarted = wi.JobStarted
		}
		if ev := wi.Event; ev != nil {
			rec.RepoFullName = ev.GetRepo().GetFullName()
			if rec.RepoURL == "" {
				rec.RepoURL = ev.GetRepo().GetHTMLURL()
			}
			if job := ev.GetWorkflowJob(); job != nil {
				rec.WorkflowName = job.GetWorkflowName()
				rec.JobName = job.GetName()
				rec.JobID = job.GetID()
				rec.RunID = job.GetRunID()
				rec.JobURL = job.GetHTMLURL()
				if rec.JobURL == "" && rec.RepoFullName != "" && rec.RunID != 0 && rec.JobID != 0 {
					rec.JobURL = fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", rec.RepoFullName, rec.RunID, rec.JobID)
				}
				if len(job.Labels) > 0 {
					rec.Labels = append([]string(nil), job.Labels...)
				}
			}
		}
	}
}
