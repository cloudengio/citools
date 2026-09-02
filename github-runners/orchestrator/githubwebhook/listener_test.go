// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubwebhook_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"cloudeng.io/webapi/clients/github"
	"cloudeng.io/webapi/operations"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubwebhook"
	gogithub "github.com/google/go-github/v89/github"
)

const waitTimeout = 2 * time.Second

// fakeRelay is a minimal stand-in for a webhooks.Relay polling endpoint. Each
// GET performs a "hanging read": it blocks until a delivery is queued (or the
// request is cancelled) and then writes the payload with the forwarded
// X-GitHub-Event header, mirroring the real relay.
type fakeRelay struct {
	server     *httptest.Server
	deliveries chan relayDelivery
	done       chan struct{}
	requests   atomic.Int64
}

type relayDelivery struct {
	status    int // non-zero, non-200 status returned instead of a payload
	eventType string
	body      []byte
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	f := &fakeRelay{
		deliveries: make(chan relayDelivery, 16),
		done:       make(chan struct{}),
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	// Cleanups run LIFO: close done first so any in-flight hanging read returns,
	// then Close the server without it blocking on that request. This keeps a
	// leaked listener (e.g. after a test failure) from hanging the whole suite.
	t.Cleanup(f.server.Close)
	t.Cleanup(func() { close(f.done) })
	return f
}

func (f *fakeRelay) serve(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	select {
	case d := <-f.deliveries:
		if d.status != 0 {
			w.WriteHeader(d.status)
			return
		}
		if d.eventType != "" {
			w.Header().Set("X-GitHub-Event", d.eventType)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(d.body)
	case <-r.Context().Done():
	case <-f.done:
	}
}

// deliver queues a delivery for the next hanging read. An empty body simulates
// the relay closing the connection without an event (a clean shutdown).
func (f *fakeRelay) deliver(eventType string, body []byte) {
	f.deliveries <- relayDelivery{eventType: eventType, body: body}
}

// deliverStatus queues an error response for the next hanging read.
func (f *fakeRelay) deliverStatus(status int) {
	f.deliveries <- relayDelivery{status: status}
}

func workflowJobBody(t *testing.T, action string, id int64) []byte {
	t.Helper()
	job := github.MockJob("owner", "repo")
	job.ID = new(id)
	body, err := json.Marshal(gogithub.WorkflowJobEvent{Action: new(action), WorkflowJob: job})
	if err != nil {
		t.Fatalf("marshal workflow_job event: %v", err)
	}
	return body
}

// newListener returns a Listener whose handler forwards received events onto the
// returned channel.
func newListener(relayURL string) (*githubwebhook.Listener, <-chan *gogithub.WorkflowJobEvent) {
	received := make(chan *gogithub.WorkflowJobEvent, 8)
	l := githubwebhook.New(relayURL, func(_ context.Context, ev *gogithub.WorkflowJobEvent) error {
		received <- ev
		return nil
	})
	return l, received
}

// runListen starts l.Listen in a goroutine and returns a channel carrying its
// eventual error.
func runListen(ctx context.Context, l *githubwebhook.Listener) <-chan error {
	errCh := make(chan error, 1)
	go func() { errCh <- l.Listen(ctx, []operations.Option(nil)) }()
	return errCh
}

func mustReturn(t *testing.T, errCh <-chan error, want error) {
	t.Helper()
	select {
	case err := <-errCh:
		if want == nil && err != nil {
			t.Fatalf("Listen returned %v, want nil", err)
		}
		if want != nil && !errors.Is(err, want) {
			t.Fatalf("Listen returned %v, want %v", err, want)
		}
	case <-time.After(waitTimeout):
		t.Fatal("Listen did not return in time")
	}
}

func TestListenReceivesEvent(t *testing.T) {
	relay := newFakeRelay(t)
	l, received := newListener(relay.server.URL)
	relay.deliver("workflow_job", workflowJobBody(t, "requested", 42))

	errCh := runListen(context.Background(), l)

	select {
	case ev := <-received:
		if got, want := ev.GetWorkflowJob().GetID(), int64(42); got != want {
			t.Errorf("WorkflowJob.ID: got %d, want %d", got, want)
		}
		if got, want := ev.GetAction(), "requested"; got != want {
			t.Errorf("Action: got %q, want %q", got, want)
		}
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for event")
	}

	l.Stop()
	mustReturn(t, errCh, nil)
}

func TestListenMultipleEvents(t *testing.T) {
	relay := newFakeRelay(t)
	l, received := newListener(relay.server.URL)
	relay.deliver("workflow_job", workflowJobBody(t, "requested", 1))
	relay.deliver("workflow_job", workflowJobBody(t, "completed", 2))

	errCh := runListen(context.Background(), l)

	// Handlers run in separate goroutines, so collect by id rather than order.
	got := map[int64]bool{}
	for len(got) < 2 {
		select {
		case ev := <-received:
			got[ev.GetWorkflowJob().GetID()] = true
		case <-time.After(waitTimeout):
			t.Fatalf("timed out; received ids %v", got)
		}
	}
	if !got[1] || !got[2] {
		t.Errorf("received ids %v, want 1 and 2", got)
	}

	l.Stop()
	mustReturn(t, errCh, nil)
}

func TestListenSkipsUnexpectedEvent(t *testing.T) {
	relay := newFakeRelay(t)
	l, received := newListener(relay.server.URL)
	// A non-workflow_job delivery must be skipped, and the reader must carry on
	// to the following workflow_job event.
	relay.deliver("workflow_run", []byte(`{"action":"requested"}`))
	relay.deliver("workflow_job", workflowJobBody(t, "queued", 7))

	errCh := runListen(context.Background(), l)

	select {
	case ev := <-received:
		if got, want := ev.GetWorkflowJob().GetID(), int64(7); got != want {
			t.Errorf("WorkflowJob.ID: got %d, want %d", got, want)
		}
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for workflow_job event")
	}

	// The skipped workflow_run event must not reach the handler.
	select {
	case ev := <-received:
		t.Errorf("handler called for unexpected event: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}

	l.Stop()
	mustReturn(t, errCh, nil)
}

func TestListenRetriesTransientErrors(t *testing.T) {
	relay := newFakeRelay(t)
	l, received := newListener(relay.server.URL)
	// The relay fails twice before delivering; the reader should retry and
	// still surface the event.
	relay.deliverStatus(http.StatusInternalServerError)
	relay.deliverStatus(http.StatusBadGateway)
	relay.deliver("workflow_job", workflowJobBody(t, "requested", 99))

	errCh := runListen(context.Background(), l)

	select {
	case ev := <-received:
		if got, want := ev.GetWorkflowJob().GetID(), int64(99); got != want {
			t.Errorf("WorkflowJob.ID: got %d, want %d", got, want)
		}
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for event after transient errors")
	}

	// Two failed reads plus the successful one.
	if got := relay.requests.Load(); got < 3 {
		t.Errorf("relay requests: got %d, want >= 3", got)
	}

	l.Stop()
	mustReturn(t, errCh, nil)
}

func TestListenRelayClosed(t *testing.T) {
	relay := newFakeRelay(t)
	l, _ := newListener(relay.server.URL)
	// An empty body signals a clean relay shutdown; Listen should return nil.
	relay.deliver("", nil)

	errCh := runListen(context.Background(), l)
	mustReturn(t, errCh, nil)
}

func TestListenStop(t *testing.T) {
	relay := newFakeRelay(t)
	l, _ := newListener(relay.server.URL)

	errCh := runListen(context.Background(), l)
	l.Stop()
	mustReturn(t, errCh, nil)

	select {
	case <-l.DoneCh():
	default:
		t.Error("DoneCh not closed after Stop")
	}
}

func TestListenContextCancelled(t *testing.T) {
	relay := newFakeRelay(t)
	l, _ := newListener(relay.server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := runListen(ctx, l)
	cancel()
	mustReturn(t, errCh, context.Canceled)
}
