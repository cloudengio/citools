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
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
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
	return fileDownloadResponse{
		filename:    meta.Filename,
		contentType: contentType,
		body:        rc,
		length:      length,
	}, nil
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
