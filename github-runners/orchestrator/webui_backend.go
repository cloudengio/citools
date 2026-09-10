// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cloudeng.io/sync/patterns"
	"cloudeng.io/vms/vmspool"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubclient"
	"github.com/cloudengio/citools/runners/macos/orchestrator/webui"
)

// webuiBackend adapts the live orchestrator state (the WorkflowEventHandler and
// its VM pools) to the webui.Backend interface consumed by the generated API
// server. It converts the orchestrator's internal snapshot types into the
// OpenAPI-generated wire types.
//
// The handler is wired in via SetHandler once the (slow) orchestrator
// initialization completes, so the web server can start serving immediately —
// /config works right away and pool/workflow data fills in once it is ready.
type webuiBackend struct {
	cfg        Config
	configPath string

	mu      sync.RWMutex
	wh      *githubclient.WorkflowEventHandler
	changes *patterns.PubSub[struct{}]
}

var _ webui.Backend = (*webuiBackend)(nil)

func newWebUIBackend(cfg Config, configPath string) *webuiBackend {
	return &webuiBackend{
		cfg:        cfg,
		configPath: configPath,
		changes:    patterns.New[struct{}](),
	}
}

// SetHandler wires in the workflow event handler once orchestrator
// initialization is complete. It notifies any early subscribers that state is
// now available and forwards the handler's change signals to the backend's own
// subscribers for the lifetime of ctx.
func (b *webuiBackend) SetHandler(ctx context.Context, wh *githubclient.WorkflowEventHandler) {
	b.mu.Lock()
	b.wh = wh
	b.mu.Unlock()
	b.changes.Publish(struct{}{})

	whCh, cancel := wh.Subscribe(ctx)
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-whCh:
				if !ok {
					return
				}
				b.changes.Publish(struct{}{})
			}
		}
	}()
}

func (b *webuiBackend) handler() *githubclient.WorkflowEventHandler {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.wh
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (b *webuiBackend) Subscribe(ctx context.Context) (<-chan struct{}, func()) {
	sub := b.changes.Subscribe(ctx, 1)
	return sub.C(), func() { b.changes.Unsubscribe(sub) }
}

func (b *webuiBackend) ConfigFile(_ context.Context) (string, []byte, error) {
	if b.configPath == "" {
		return "", nil, fmt.Errorf("no configuration file path is known")
	}
	content, err := os.ReadFile(b.configPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read configuration file: %w", err)
	}
	return b.configPath, content, nil
}

func (b *webuiBackend) ConfigSummary(_ context.Context) (webui.ConfigSummary, error) {
	g := b.cfg.Global
	summary := webui.ConfigSummary{
		ConfigFile: b.configPath,
		Global: webui.GlobalConfigSummary{
			TmpDir:                      strPtr(g.TmpDir),
			CompletionQueueSize:         new(g.CompletionQueueSize),
			FailedVmRetentionPeriod:     strPtr(g.FailedVMRetentionPeriod.String()),
			SuccessfulVmRetentionPeriod: strPtr(g.SuccessfulVMRetentionPeriod.String()),
		},
		Webhook: webui.WebhookSummary{RelayUrl: strPtr(b.cfg.Webhook.RelayURL)},
	}
	for _, repo := range b.cfg.Repositories {
		rs := webui.RepositorySummary{
			Owner: strPtr(repo.Service.Owner),
			Repo:  strPtr(repo.Service.Repo),
		}
		var runners []webui.RunnerSummary
		for _, r := range repo.Runners {
			runners = append(runners, webui.RunnerSummary{
				NamePrefix: strPtr(r.NamePrefix),
				Labels:     new(append([]string(nil), r.Labels...)),
				VmPool:     strPtr(r.VMPoolName),
			})
		}
		if runners != nil {
			rs.Runners = &runners
		}
		summary.Repositories = append(summary.Repositories, rs)
	}
	for name, pool := range b.cfg.VMPools {
		image := ""
		if pool.Tart != nil {
			image = pool.Tart.Image
		}
		summary.Pools = append(summary.Pools, webui.PoolConfigSummary{
			Name:      strPtr(name),
			Image:     strPtr(image),
			Size:      new(pool.Size),
			RunnerDir: strPtr(pool.RunnerDir()),
		})
	}
	return summary, nil
}

func (b *webuiBackend) Pools(ctx context.Context) ([]webui.PoolStatus, error) {
	wh := b.handler()
	if wh == nil {
		return []webui.PoolStatus{}, nil
	}
	snaps, err := wh.PoolStatus(ctx)
	if err != nil {
		return nil, err
	}
	acquired := b.acquiredVMIDs()
	out := make([]webui.PoolStatus, 0, len(snaps))
	for _, p := range snaps {
		ps := webui.PoolStatus{
			Name:  p.Name,
			Kind:  strPtr(p.Kind),
			Image: strPtr(p.Image),
			Size:  p.Size,
			Vms:   make([]webui.VMStatus, 0, len(p.VMs)),
		}
		for _, vm := range p.VMs {
			ps.Vms = append(ps.Vms, webui.VMStatus{
				Id:        vm.Name,
				Name:      strPtr(vm.Name),
				Pool:      strPtr(p.Name),
				State:     mapVMState(vm, acquired[vm.Name]),
				UpdatedAt: timePtr(vm.Accessed),
				LastEvent: strPtr(vm.State),
			})
		}
		out = append(out, ps)
	}
	return out, nil
}

// acquiredVMIDs returns the set of VM ids that are currently backing a live
// (non-terminal) workflow job. A VM's id equals its tart VM name, so this is the
// authoritative source for distinguishing acquired VMs from idle warm ones — the
// pool/"tart list" view alone cannot tell them apart.
func (b *webuiBackend) acquiredVMIDs() map[string]bool {
	acquired := map[string]bool{}
	wh := b.handler()
	if wh == nil {
		return acquired
	}
	for _, wf := range wh.Workflows() {
		switch wf.State {
		case githubclient.WorkflowRunning, githubclient.WorkflowAcquiring:
			if wf.VMID != "" {
				acquired[wf.VMID] = true
			}
		}
	}
	return acquired
}

// mapVMState maps the coarse "tart list" state onto the API's VMState enum,
// using the acquired flag (derived from live workflows) to distinguish an
// in-use VM from an idle warm one.
func mapVMState(vm vmspool.VMInfo, acquired bool) webui.VMState {
	switch {
	case vm.Running && acquired:
		return webui.VMStateAcquired
	case vm.Running:
		return webui.VMStateAvailable
	case strings.EqualFold(vm.State, "suspended"):
		return webui.VMStateStaging
	case strings.EqualFold(vm.State, "stopped"):
		return webui.VMStateStopped
	default:
		return webui.VMStateUnknown
	}
}

func (b *webuiBackend) Workflows(_ context.Context) ([]webui.WorkflowStatus, error) {
	wh := b.handler()
	if wh == nil {
		return []webui.WorkflowStatus{}, nil
	}
	snaps := wh.Workflows()
	out := make([]webui.WorkflowStatus, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, workflowStatusFromSnapshot(s))
	}
	return out, nil
}

func (b *webuiBackend) CancelWorkflow(ctx context.Context, name string) error {
	wh := b.handler()
	if wh == nil {
		return fmt.Errorf("orchestrator is still initializing")
	}
	reqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := wh.Cancel(reqCtx, name); err != nil {
		if errors.Is(err, githubclient.ErrWorkflowNotRunning) {
			return webui.ErrWorkflowNotFound
		}
		return err
	}
	return nil
}

func (b *webuiBackend) Workflow(_ context.Context, name string) (webui.WorkflowStatus, bool, error) {
	wh := b.handler()
	if wh == nil {
		return webui.WorkflowStatus{}, false, nil
	}
	s, ok := wh.Workflow(name)
	if !ok {
		return webui.WorkflowStatus{}, false, nil
	}
	return workflowStatusFromSnapshot(s), true, nil
}

func workflowStatusFromSnapshot(s githubclient.WorkflowSnapshot) webui.WorkflowStatus {
	ws := webui.WorkflowStatus{
		Name:          s.Name,
		State:         webui.WorkflowState(s.State),
		RepoFullName:  strPtr(s.RepoFullName),
		RepoUrl:       strPtr(s.RepoURL),
		WorkflowName:  strPtr(s.WorkflowName),
		JobName:       strPtr(s.JobName),
		Pool:          strPtr(s.Pool),
		VmId:          strPtr(s.VMID),
		Result:        strPtr(s.Result),
		Error:         strPtr(s.Err),
		QueuedAt:      timePtr(s.QueuedAt),
		StartedAt:     timePtr(s.StartedAt),
		VmCompletedAt: timePtr(s.VMCompletedAt),
		CompletedAt:   timePtr(s.CompletedAt),
	}
	if s.JobID != 0 {
		ws.JobId = new(s.JobID)
	}
	if s.RunID != 0 {
		ws.RunId = new(s.RunID)
	}
	jobURL := s.JobURL
	if jobURL == "" && s.RepoFullName != "" && s.RunID != 0 && s.JobID != 0 {
		jobURL = fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", s.RepoFullName, s.RunID, s.JobID)
	}
	if jobURL != "" {
		ws.JobUrl = &jobURL
	}
	if len(s.Labels) > 0 {
		ws.Labels = new(append([]string(nil), s.Labels...))
	}
	var logs []webui.LogArtifact
	if art, ok := logArtifact(s.Name, "job", s.JobLogPath); ok {
		logs = append(logs, art)
	}
	if art, ok := logArtifact(s.Name, "diag", s.DiagLogPath); ok {
		logs = append(logs, art)
	}
	if logs != nil {
		ws.Logs = &logs
	}
	return ws
}

// logArtifact builds the metadata for a downloadable log file, or reports false
// when path is empty (the artifact does not exist for this workflow).
func logArtifact(wfName, id, path string) (webui.LogArtifact, bool) {
	if path == "" {
		return webui.LogArtifact{}, false
	}
	art := webui.LogArtifact{
		Id:          id,
		Filename:    filepath.Base(path),
		ContentType: strPtr(contentTypeForLog(path)),
		Href:        fmt.Sprintf("%s/workflows/%s/logs/%s", webui.BasePath, url.PathEscape(wfName), id),
	}
	if fi, err := os.Stat(path); err == nil {
		art.SizeBytes = new(fi.Size())
	}
	return art, true
}

func contentTypeForLog(path string) string {
	if strings.HasSuffix(path, ".tar.gz") || strings.HasSuffix(path, ".tgz") {
		return "application/gzip"
	}
	return "text/plain; charset=utf-8"
}

func (b *webuiBackend) WorkflowLog(_ context.Context, name, artifact string) (io.ReadCloser, webui.LogArtifact, error) {
	wh := b.handler()
	if wh == nil {
		return nil, webui.LogArtifact{}, fmt.Errorf("orchestrator is still initializing")
	}
	s, ok := wh.Workflow(name)
	if !ok {
		return nil, webui.LogArtifact{}, fmt.Errorf("no such workflow: %s", name)
	}
	var path string
	switch artifact {
	case "job":
		path = s.JobLogPath
	case "diag":
		path = s.DiagLogPath
	default:
		return nil, webui.LogArtifact{}, fmt.Errorf("no such artifact %q for workflow %s", artifact, name)
	}
	if path == "" {
		return nil, webui.LogArtifact{}, fmt.Errorf("artifact %q not available for workflow %s", artifact, name)
	}
	// path originates from the orchestrator's own LogFileManager, not user input;
	// artifact only selects between the two known internal paths.
	f, err := os.Open(path) //nolint:gosec // G304: path is orchestrator-controlled.
	if err != nil {
		return nil, webui.LogArtifact{}, err
	}
	art, _ := logArtifact(name, artifact, path)
	return f, art, nil
}

func (b *webuiBackend) ServiceStatus(_ context.Context) (webui.ServiceStatus, error) {
	installed := serviceAgent().IsInstalled()
	lbl := serviceLabel
	return webui.ServiceStatus{
		Installed: installed,
		Running:   installed,
		Label:     &lbl,
	}, nil
}

func (b *webuiBackend) RestartService(_ context.Context) error {
	agent := serviceAgent()
	if !agent.IsInstalled() {
		return errors.New("login service is not installed")
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = runSteps(context.Background(), false, agent.Restart())
	}()
	return nil
}

func (b *webuiBackend) UninstallService(_ context.Context) error {
	agent := serviceAgent()
	if !agent.IsInstalled() {
		return errors.New("login service is not installed")
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = runSteps(context.Background(), false, agent.Uninstall()...)
		os.Exit(0)
	}()
	return nil
}

func (b *webuiBackend) BuildInfo(_ context.Context) (webui.BuildInfo, error) {
	return webui.CurrentBuildInfo(), nil
}
