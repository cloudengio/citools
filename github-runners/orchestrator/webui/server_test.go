// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package webui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeBackend struct {
	changes chan struct{}
}

func newFakeBackend() *fakeBackend { return &fakeBackend{changes: make(chan struct{}, 1)} }

func (fakeBackend) ConfigSummary(context.Context) (ConfigSummary, error) {
	return ConfigSummary{ConfigFile: "/tmp/cfg.yml"}, nil
}

func (fakeBackend) ConfigFile(context.Context) (string, []byte, error) {
	return "/tmp/cfg.yml", []byte("global: {}\n"), nil
}

func (fakeBackend) Pools(context.Context) ([]PoolStatus, error) {
	return []PoolStatus{{
		Name: "macos", Size: 2,
		Vms: []VMStatus{{Id: "vm1", State: VMStateAvailable}},
	}}, nil
}

func workflowWithLogs() WorkflowStatus {
	logs := []LogArtifact{{Id: "job", Filename: "job.txt", Href: BasePath + "/workflows/w1/logs/job"}}
	return WorkflowStatus{Name: "w1", State: WorkflowStateRunning, VmId: new("vm1"), Logs: &logs}
}

//go:fix inline
func strp(s string) *string { return new(s) }

func (fakeBackend) Workflows(context.Context) ([]WorkflowStatus, error) {
	return []WorkflowStatus{workflowWithLogs()}, nil
}

func (fakeBackend) Workflow(_ context.Context, name string) (WorkflowStatus, bool, error) {
	if name != "w1" {
		return WorkflowStatus{}, false, nil
	}
	return workflowWithLogs(), true, nil
}

func (fakeBackend) CancelWorkflow(_ context.Context, name string) error {
	if name != "w1" {
		return ErrWorkflowNotFound
	}
	return nil
}

func (fakeBackend) WorkflowLog(_ context.Context, _, artifact string) (io.ReadCloser, LogArtifact, error) {
	return io.NopCloser(strings.NewReader("hello log")), LogArtifact{Id: artifact, Filename: "job.txt"}, nil
}

func (f *fakeBackend) Subscribe(context.Context) (<-chan struct{}, func()) {
	return f.changes, func() {}
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(NewServer(newFakeBackend()).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestEndpoints(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct{ path, want string }{
		{BasePath + "/config", `"config_file":"/tmp/cfg.yml"`},
		{BasePath + "/config/file", "global:"},
		{BasePath + "/pools", `"name":"macos"`},
		{BasePath + "/workflows", `"name":"w1"`},
		{BasePath + "/workflows/w1", `"state":"running"`},
		{BasePath + "/workflows/w1/logs", `"id":"job"`},
		{BasePath + "/workflows/w1/logs/job", "hello log"},
		{"/", "Runner Orchestrator"},
	}
	for _, tc := range cases {
		code, body := get(t, ts.URL+tc.path)
		if code != http.StatusOK {
			t.Errorf("%s: status %d body=%s", tc.path, code, body)
		}
		if !strings.Contains(body, tc.want) {
			t.Errorf("%s: body %q missing %q", tc.path, body, tc.want)
		}
	}
}

func TestCancelWorkflow(t *testing.T) {
	ts := newTestServer(t)
	post := func(path string) int {
		resp, err := http.Post(ts.URL+path, "", nil) //nolint:noctx
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(BasePath + "/workflows/w1/cancel"); code != http.StatusAccepted {
		t.Errorf("cancel w1: status %d, want 202", code)
	}
	if code := post(BasePath + "/workflows/nope/cancel"); code != http.StatusNotFound {
		t.Errorf("cancel nope: status %d, want 404", code)
	}
}

func TestWorkflowNotFound(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := get(t, ts.URL+BasePath+"/workflows/nope"); code != http.StatusNotFound {
		t.Errorf("missing workflow: got %d want 404", code)
	}
}

func TestConfigFileDownloadHeaders(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + BasePath + "/config/file") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "cfg.yml") {
		t.Errorf("Content-Disposition = %q, want filename cfg.yml", cd)
	}
}

func TestListWorkflowsStateFilter(t *testing.T) {
	ts := newTestServer(t)
	code, body := get(t, ts.URL+BasePath+"/workflows?state=completed")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if strings.Contains(body, "w1") {
		t.Errorf("state=completed should exclude the running w1, got %s", body)
	}
	if body := strings.TrimSpace(body); body != "[]" && body != "null" {
		t.Errorf("expected empty result, got %s", body)
	}
}

func TestSSEHelloAndSnapshot(t *testing.T) {
	ts := newTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+BasePath+"/events", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}
	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)
	frame := string(buf[:n])
	if !strings.Contains(frame, "event: hello") {
		t.Errorf("first SSE frame missing hello: %q", frame)
	}
}
