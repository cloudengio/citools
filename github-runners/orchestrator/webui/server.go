// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// ErrWorkflowNotFound is returned by Backend.CancelWorkflow when no running
// workflow with the given name exists.
var ErrWorkflowNotFound = errors.New("no such running workflow")

// BasePath is the URL prefix under which the JSON API is served.
const BasePath = "/api/v1"

// Backend supplies the live orchestrator state that the API serves. It is
// implemented by the run command over the WorkflowEventHandler and VM pools.
// All methods must be safe for concurrent use.
type Backend interface {
	// ConfigSummary returns a structured summary of the running configuration.
	ConfigSummary(ctx context.Context) (ConfigSummary, error)
	// ConfigFile returns the path and raw bytes of the loaded YAML config file.
	ConfigFile(ctx context.Context) (path string, content []byte, err error)
	// Pools returns the current state of every configured VM pool.
	Pools(ctx context.Context) ([]PoolStatus, error)
	// Workflows returns every running and recently-completed workflow job, with
	// each job's log artifacts populated.
	Workflows(ctx context.Context) ([]WorkflowStatus, error)
	// Workflow returns a single workflow job by runner-instance name.
	Workflow(ctx context.Context, name string) (WorkflowStatus, bool, error)
	// CancelWorkflow cancels a running workflow job by runner-instance name,
	// which also tears down its VM. It returns ErrWorkflowNotFound if no such
	// running workflow exists.
	CancelWorkflow(ctx context.Context, name string) error
	// WorkflowLog opens a log artifact for a workflow job. The caller closes the
	// returned reader.
	WorkflowLog(ctx context.Context, name, artifact string) (io.ReadCloser, LogArtifact, error)
	// ServiceStatus returns the status of the launchd login service.
	ServiceStatus(ctx context.Context) (ServiceStatus, error)
	// RestartService requests restarting the orchestrator login service.
	RestartService(ctx context.Context) error
	// UninstallService requests uninstalling and stopping the orchestrator login service.
	UninstallService(ctx context.Context) error
	// BuildInfo returns the orchestrator binary build and version details.
	BuildInfo(ctx context.Context) (BuildInfo, error)
	// Subscribe returns a coalescing change signal and a cancel function. The
	// subscription is also released when ctx is cancelled.
	Subscribe(ctx context.Context) (<-chan struct{}, func())
}

// Server implements StrictServerInterface over a Backend.
type Server struct {
	backend Backend
	assets  fs.FS
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithAssets overrides the SPA asset filesystem served at the root (see
// FrontendAssets), e.g. to enable live reloading from the local filesystem.
func WithAssets(assets fs.FS) ServerOption {
	return func(s *Server) { s.assets = assets }
}

// NewServer returns a Server serving state from backend. By default the embedded
// SPA build is served; use WithAssets to supply a reloadable asset filesystem.
func NewServer(backend Backend, opts ...ServerOption) *Server {
	s := &Server{backend: backend, assets: FrontendAssets()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// APIHandler returns an http.Handler that serves the JSON API under BasePath.
func (s *Server) APIHandler() http.Handler {
	strict := NewStrictHandler(s, nil)
	return HandlerWithOptions(strict, StdHTTPServerOptions{BaseURL: BasePath})
}

var _ StrictServerInterface = (*Server)(nil)

func (s *Server) GetConfig(ctx context.Context, _ GetConfigRequestObject) (GetConfigResponseObject, error) {
	cfg, err := s.backend.ConfigSummary(ctx)
	if err != nil {
		return nil, err
	}
	return GetConfig200JSONResponse(cfg), nil
}

func (s *Server) GetBuildInfo(ctx context.Context, _ GetBuildInfoRequestObject) (GetBuildInfoResponseObject, error) {
	bi, err := s.backend.BuildInfo(ctx)
	if err != nil {
		return nil, err
	}
	return GetBuildInfo200JSONResponse(bi), nil
}

func (s *Server) DownloadConfigFile(ctx context.Context, _ DownloadConfigFileRequestObject) (DownloadConfigFileResponseObject, error) {
	path, content, err := s.backend.ConfigFile(ctx)
	if err != nil {
		return DownloadConfigFile404JSONResponse{Error: err.Error()}, nil
	}
	return fileDownloadResponse{
		filename:    filepath.Base(path),
		contentType: "application/yaml",
		body:        bytes.NewReader(content),
		length:      int64(len(content)),
	}, nil
}

func (s *Server) ListPools(ctx context.Context, _ ListPoolsRequestObject) (ListPoolsResponseObject, error) {
	pools, err := s.backend.Pools(ctx)
	if err != nil {
		return nil, err
	}
	return ListPools200JSONResponse(pools), nil
}

func (s *Server) ListWorkflows(ctx context.Context, request ListWorkflowsRequestObject) (ListWorkflowsResponseObject, error) {
	wfs, err := s.backend.Workflows(ctx)
	if err != nil {
		return nil, err
	}
	if request.Params.State != nil {
		filtered := wfs[:0]
		for _, wf := range wfs {
			if wf.State == *request.Params.State {
				filtered = append(filtered, wf)
			}
		}
		wfs = filtered
	}
	return ListWorkflows200JSONResponse(wfs), nil
}

func (s *Server) GetWorkflow(ctx context.Context, request GetWorkflowRequestObject) (GetWorkflowResponseObject, error) {
	wf, ok, err := s.backend.Workflow(ctx, request.Name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetWorkflow404JSONResponse{Error: "no such workflow: " + request.Name}, nil
	}
	return GetWorkflow200JSONResponse(wf), nil
}

func (s *Server) CancelWorkflow(ctx context.Context, request CancelWorkflowRequestObject) (CancelWorkflowResponseObject, error) {
	switch err := s.backend.CancelWorkflow(ctx, request.Name); {
	case err == nil:
		return CancelWorkflow202Response{}, nil
	case errors.Is(err, ErrWorkflowNotFound):
		return CancelWorkflow404JSONResponse{Error: "no such running workflow: " + request.Name}, nil
	default:
		return CancelWorkflow500JSONResponse{Error: err.Error()}, nil
	}
}

func (s *Server) ListWorkflowLogs(ctx context.Context, request ListWorkflowLogsRequestObject) (ListWorkflowLogsResponseObject, error) {
	wf, ok, err := s.backend.Workflow(ctx, request.Name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ListWorkflowLogs404JSONResponse{Error: "no such workflow: " + request.Name}, nil
	}
	logs := []LogArtifact{}
	if wf.Logs != nil {
		logs = *wf.Logs
	}
	return ListWorkflowLogs200JSONResponse(logs), nil
}

func (s *Server) DownloadWorkflowLog(ctx context.Context, request DownloadWorkflowLogRequestObject) (DownloadWorkflowLogResponseObject, error) {
	rc, meta, err := s.backend.WorkflowLog(ctx, request.Name, request.Artifact)
	if err != nil {
		return DownloadWorkflowLog404JSONResponse{Error: err.Error()}, nil
	}
	var length int64
	if meta.SizeBytes != nil {
		length = *meta.SizeBytes
	}
	contentType := "application/octet-stream"
	if meta.ContentType != nil && *meta.ContentType != "" {
		contentType = *meta.ContentType
	}
	if request.Params.View != nil && *request.Params.View && (strings.HasPrefix(contentType, "text/") || request.Artifact == "job") {
		var jobURL string
		if request.Params.JobUrl != nil && *request.Params.JobUrl != "" {
			jobURL = *request.Params.JobUrl
		} else if wf, ok, err := s.backend.Workflow(ctx, request.Name); err == nil && ok {
			if wf.JobUrl != nil && *wf.JobUrl != "" {
				jobURL = *wf.JobUrl
			} else if wf.RepoFullName != nil && wf.RunId != nil && wf.JobId != nil && *wf.RepoFullName != "" && *wf.RunId != 0 && *wf.JobId != 0 {
				jobURL = fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", *wf.RepoFullName, *wf.RunId, *wf.JobId)
			}
		}
		return workflowLogHTMLResponse{
			workflowName: request.Name,
			artifact:     request.Artifact,
			filename:     meta.Filename,
			body:         rc,
			jobURL:       jobURL,
		}, nil
	}
	return fileDownloadResponse{
		filename:    meta.Filename,
		contentType: contentType,
		body:        rc,
		length:      length,
	}, nil
}

func (s *Server) GetServiceStatus(ctx context.Context, _ GetServiceStatusRequestObject) (GetServiceStatusResponseObject, error) {
	status, err := s.backend.ServiceStatus(ctx)
	if err != nil {
		return nil, err
	}
	return GetServiceStatus200JSONResponse(status), nil
}

func (s *Server) RestartService(ctx context.Context, _ RestartServiceRequestObject) (RestartServiceResponseObject, error) {
	if err := s.backend.RestartService(ctx); err != nil {
		return RestartService400JSONResponse{Error: err.Error()}, nil
	}
	return RestartService200Response{}, nil
}

func (s *Server) UninstallService(ctx context.Context, _ UninstallServiceRequestObject) (UninstallServiceResponseObject, error) {
	if err := s.backend.UninstallService(ctx); err != nil {
		return UninstallService400JSONResponse{Error: err.Error()}, nil
	}
	return UninstallService200Response{}, nil
}

func (s *Server) StreamEvents(ctx context.Context, _ StreamEventsRequestObject) (StreamEventsResponseObject, error) {
	pr, pw := io.Pipe()
	go s.pumpEvents(ctx, pw)
	return StreamEvents200TexteventStreamResponse{Body: pr}, nil
}

// pumpEvents writes SSE frames to pw until ctx is done. It emits a full snapshot
// on connect and whenever the backend signals a change, plus a periodic
// heartbeat to keep the connection alive.
func (s *Server) pumpEvents(ctx context.Context, pw *io.PipeWriter) {
	changes, cancel := s.backend.Subscribe(ctx)
	defer cancel()
	defer pw.Close() //nolint:errcheck

	if err := writeEvent(pw, Event{Type: Hello, Timestamp: time.Now()}); err != nil {
		return
	}
	if err := s.writeSnapshot(ctx, pw); err != nil {
		return
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
			if err := s.writeSnapshot(ctx, pw); err != nil {
				return
			}
		case <-heartbeat.C:
			if _, err := io.WriteString(pw, ": ping\n\n"); err != nil {
				return
			}
		}
	}
}

// writeSnapshot emits one pool event per pool and one workflow event per
// workflow, so a client can fully reconcile its view from the stream.
func (s *Server) writeSnapshot(ctx context.Context, pw *io.PipeWriter) error {
	now := time.Now()
	pools, err := s.backend.Pools(ctx)
	if err == nil {
		for i := range pools {
			if werr := writeEvent(pw, Event{Type: Pool, Timestamp: now, Pool: &pools[i]}); werr != nil {
				return werr
			}
		}
	}
	wfs, err := s.backend.Workflows(ctx)
	if err == nil {
		for i := range wfs {
			if werr := writeEvent(pw, Event{Type: Workflow, Timestamp: now, Workflow: &wfs[i]}); werr != nil {
				return werr
			}
		}
	}
	return nil
}

func writeEvent(w io.Writer, e Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data)
	return err
}

// fileDownloadResponse is a download response that sets a Content-Disposition
// filename. It satisfies both the config-file and workflow-log download
// response interfaces so a single type serves every binary download.
type fileDownloadResponse struct {
	filename    string
	contentType string
	body        io.Reader
	length      int64
}

func (r fileDownloadResponse) write(w http.ResponseWriter) error {
	if r.contentType != "" {
		w.Header().Set("Content-Type", r.contentType)
	}
	if r.filename != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", r.filename))
	}
	if r.length > 0 {
		w.Header().Set("Content-Length", fmt.Sprint(r.length))
	}
	w.WriteHeader(http.StatusOK)
	if c, ok := r.body.(io.Closer); ok {
		defer c.Close() //nolint:errcheck
	}
	_, err := io.Copy(w, r.body)
	return err
}

func (r fileDownloadResponse) VisitDownloadConfigFileResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r fileDownloadResponse) VisitDownloadWorkflowLogResponse(w http.ResponseWriter) error {
	return r.write(w)
}

type workflowLogHTMLResponse struct {
	workflowName string
	artifact     string
	filename     string
	body         io.Reader
	jobURL       string
}

func (r workflowLogHTMLResponse) VisitDownloadWorkflowLogResponse(w http.ResponseWriter) error {
	if c, ok := r.body.(io.Closer); ok {
		defer c.Close() //nolint:errcheck
	}
	data, err := io.ReadAll(r.body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return logPageTemplate.Execute(w, struct {
		Name     string
		Artifact string
		Filename string
		Content  string
		JobURL   string
	}{
		Name:     r.workflowName,
		Artifact: r.artifact,
		Filename: r.filename,
		Content:  string(data),
		JobURL:   r.jobURL,
	})
}

var logPageTemplate = template.Must(template.New("logPage").Parse(`<!DOCTYPE html>
<html lang="en" style="background-color: #ffffff; color-scheme: light;">
<head>
  <meta charset="utf-8">
  <meta name="color-scheme" content="light">
  <meta name="theme-color" content="#ffffff">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Workflow Log: {{.Name}} ({{.Artifact}})</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #ffffff;
      --panel: #f6f8fa;
      --panel2: #ffffff;
      --fg: #1f2328;
      --muted: #656d76;
      --border: #d0d7de;
      --accent: #0969da;
    }
    * { box-sizing: border-box; }
    html {
      background-color: #ffffff;
      color-scheme: light;
    }
    body {
      margin: 0;
      background-color: #ffffff;
      color: var(--fg);
      font: 13px/1.5 ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
      min-height: 100vh;
    }
    .topbar {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      padding: 12px 20px;
      background: var(--panel);
      border-bottom: 1px solid var(--border);
      position: sticky;
      top: 0;
      z-index: 10;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    }
    .title {
      font-size: 14px;
      color: var(--muted);
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .title strong {
      color: var(--fg);
    }
    .actions {
      display: flex;
      align-items: center;
      gap: 8px;
      flex-shrink: 0;
    }
    .btn {
      background: var(--panel2);
      border: 1px solid var(--border);
      color: var(--fg);
      border-radius: 6px;
      padding: 4px 12px;
      font-size: 12px;
      cursor: pointer;
      text-decoration: none;
      font-family: inherit;
      display: inline-flex;
      align-items: center;
      gap: 4px;
    }
    .btn:hover {
      border-color: var(--accent);
      color: var(--accent);
      background: #f3f4f6;
    }
    .btn.primary {
      background: #0969da;
      border-color: #0969da;
      color: #ffffff;
    }
    .btn.primary:hover {
      background: #085bc4;
      border-color: #085bc4;
      color: #ffffff;
    }
    .log-content {
      padding: 16px 20px;
      margin: 0;
      white-space: pre-wrap;
      word-break: break-all;
      background-color: #ffffff;
      color: var(--fg);
    }
  </style>
</head>
<body style="background-color: #ffffff; color: #1f2328;">
  <div class="topbar">
    <div class="title">
      Workflow: <strong>{{.Name}}</strong> &middot; Artifact: <strong>{{.Filename}}</strong>
      {{if .JobURL}}
      &middot; <a href="{{.JobURL}}" target="_blank" rel="noopener noreferrer" style="color: var(--accent); text-decoration: none; font-weight: 500;">GitHub workflow log ↗</a>
      {{end}}
    </div>
    <div class="actions">
      {{if .JobURL}}
      <a class="btn primary" href="{{.JobURL}}" target="_blank" rel="noopener noreferrer" title="View workflow log on GitHub">GitHub Log ↗</a>
      {{end}}
      <button class="btn" onclick="location.reload()">Refresh</button>
      <a class="btn" href="?view=false" download>Download</a>
    </div>
  </div>
  <pre class="log-content">{{.Content}}</pre>
</body>
</html>
`))
