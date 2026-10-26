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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloudeng.io/webapi/clients/github"
	"cloudeng.io/webapi/operations"
	"github.com/cloudengio/citools/runners/macos/orchestrator/githubwebhook"
	gogithub "github.com/google/go-github/v89/github"
)

const waitTimeout = 2 * time.Second

// fakeRelay is a minimal stand-in for a webhooks.Relay endpoint. It supports
// both the streaming protocol (when stream=1 is requested) and the long-polling
// protocol (when stream=1 is omitted).
type fakeRelay struct {
	server     *httptest.Server
	deliveries chan relayDelivery
	done       chan struct{}
	requests   atomic.Int64
	// ended carries one value per request the relay has finished serving, ie.
	// per connection the listener no longer holds.
	ended chan struct{}
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
		ended:      make(chan struct{}, 16),
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
	defer func() {
		select {
		case f.ended <- struct{}{}:
		default:
		}
	}()
	if r.URL.Query().Get("stream") == "1" {
		f.serveStream(w, r)
		return
	}
	f.servePoll(w, r)
}

func (f *fakeRelay) servePoll(w http.ResponseWriter, r *http.Request) {
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

func (f *fakeRelay) serveStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "flusher not supported", http.StatusInternalServerError)
		return
	}
	enc := json.NewEncoder(w)
	first := true
	for {
		select {
		case d := <-f.deliveries:
			if d.status != 0 {
				w.WriteHeader(d.status)
				return
			}
			if len(d.body) == 0 {
				if first {
					w.WriteHeader(http.StatusOK)
				}
				return
			}
			if first {
				w.Header().Set("Content-Type", "application/json")
				first = false
			}
			h := http.Header{}
			if d.eventType != "" {
				h.Set("X-GitHub-Event", d.eventType)
			}
			type streamDelivery struct {
				Headers http.Header     `json:"headers"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := enc.Encode(streamDelivery{Headers: h, Payload: d.body}); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-f.done:
			return
		}
	}
}

// deliver queues a delivery for the stream or next hanging read. An empty body
// simulates the relay closing the connection without an event (a clean shutdown).
func (f *fakeRelay) deliver(eventType string, body []byte) {
	f.deliveries <- relayDelivery{eventType: eventType, body: body}
}

// deliverStatus queues an error response for the stream or next hanging read.
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
func newListener(relayURL string, opts ...githubwebhook.Option) (*githubwebhook.Listener, <-chan *gogithub.WorkflowJobEvent) {
	received := make(chan *gogithub.WorkflowJobEvent, 8)
	l := githubwebhook.New(relayURL, func(_ context.Context, ev *gogithub.WorkflowJobEvent) error {
		received <- ev
		return nil
	}, opts...)
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

func testModes(t *testing.T, fn func(t *testing.T, opts ...githubwebhook.Option)) {
	t.Helper()
	t.Run("polling", func(t *testing.T) {
		fn(t, githubwebhook.WithStreaming(false))
	})
	t.Run("streaming", func(t *testing.T) {
		fn(t, githubwebhook.WithStreaming(true))
	})
}

func TestListenReceivesEvent(t *testing.T) {
	testModes(t, func(t *testing.T, opts ...githubwebhook.Option) {
		relay := newFakeRelay(t)
		l, received := newListener(relay.server.URL, opts...)
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
	})
}

func TestListenMultipleEvents(t *testing.T) {
	testModes(t, func(t *testing.T, opts ...githubwebhook.Option) {
		relay := newFakeRelay(t)
		l, received := newListener(relay.server.URL, opts...)
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
	})
}

func TestListenSkipsUnexpectedEvent(t *testing.T) {
	testModes(t, func(t *testing.T, opts ...githubwebhook.Option) {
		relay := newFakeRelay(t)
		l, received := newListener(relay.server.URL, opts...)
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
	})
}

func TestListenRetriesTransientErrors(t *testing.T) {
	testModes(t, func(t *testing.T, opts ...githubwebhook.Option) {
		relay := newFakeRelay(t)
		l, received := newListener(relay.server.URL, opts...)
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
	})
}

func TestListenRelayClosed(t *testing.T) {
	testModes(t, func(t *testing.T, opts ...githubwebhook.Option) {
		relay := newFakeRelay(t)
		l, _ := newListener(relay.server.URL, opts...)
		// An empty body signals a clean relay shutdown; Listen should return nil.
		relay.deliver("", nil)

		errCh := runListen(context.Background(), l)
		mustReturn(t, errCh, nil)
	})
}

func TestListenStop(t *testing.T) {
	testModes(t, func(t *testing.T, opts ...githubwebhook.Option) {
		relay := newFakeRelay(t)
		l, _ := newListener(relay.server.URL, opts...)

		errCh := runListen(context.Background(), l)
		l.Stop()
		mustReturn(t, errCh, nil)

		select {
		case <-l.DoneCh():
		default:
			t.Error("DoneCh not closed after Stop")
		}
	})
}

func TestListenContextCancelled(t *testing.T) {
	testModes(t, func(t *testing.T, opts ...githubwebhook.Option) {
		relay := newFakeRelay(t)
		l, _ := newListener(relay.server.URL, opts...)

		ctx, cancel := context.WithCancel(context.Background())
		errCh := runListen(ctx, l)
		cancel()
		mustReturn(t, errCh, context.Canceled)
	})
}

// fakeCapacity is a githubwebhook.Capacity whose answer the test controls. It
// signals a change as the orchestrator's VM pools do: on a channel whose
// signals may be coalesced.
type fakeCapacity struct {
	mu        sync.Mutex
	available bool
	changed   chan struct{}
}

func newFakeCapacity(available bool) *fakeCapacity {
	return &fakeCapacity{available: available, changed: make(chan struct{}, 1)}
}

func (c *fakeCapacity) HasCapacity() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.available
}

func (c *fakeCapacity) Changed() <-chan struct{} {
	return c.changed
}

// set changes the answer and signals that it has changed.
func (c *fakeCapacity) set(available bool) {
	c.mu.Lock()
	c.available = available
	c.mu.Unlock()
	select {
	case c.changed <- struct{}{}:
	default:
	}
}

// withCapacity returns opts plus the capacity to gate the listener on.
func withCapacity(c *fakeCapacity, opts []githubwebhook.Option) []githubwebhook.Option {
	return append([]githubwebhook.Option{githubwebhook.WithCapacity(c)}, opts...)
}

// waitForRequests waits until the relay has been sent at least n requests.
func waitForRequests(t *testing.T, relay *fakeRelay, n int64) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for relay.requests.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d relay requests: got %d", n, relay.requests.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// assertNoRequests checks that the relay is left alone for d, ie. that the
// listener is not reconnecting to it.
func assertNoRequests(t *testing.T, relay *fakeRelay, d time.Duration) {
	t.Helper()
	before := relay.requests.Load()
	time.Sleep(d)
	if got := relay.requests.Load(); got != before {
		t.Errorf("the relay was read whilst there was no capacity: requests went from %d to %d", before, got)
	}
}

func TestListenWaitsForCapacity(t *testing.T) {
	testModes(t, func(t *testing.T, opts ...githubwebhook.Option) {
		relay := newFakeRelay(t)
		capacity := newFakeCapacity(false)
		l, received := newListener(relay.server.URL, withCapacity(capacity, opts)...)
		relay.deliver("workflow_job", workflowJobBody(t, "queued", 11))

		errCh := runListen(context.Background(), l)

		// With nothing to run a job on the relay is not read at all: the event
		// stays queued there rather than being taken on and left to wait.
		assertNoRequests(t, relay, 200*time.Millisecond)
		select {
		case ev := <-received:
			t.Fatalf("the handler was called with no capacity for the job: %+v", ev)
		default:
		}

		capacity.set(true)
		select {
		case ev := <-received:
			if got, want := ev.GetWorkflowJob().GetID(), int64(11); got != want {
				t.Errorf("WorkflowJob.ID: got %d, want %d", got, want)
			}
		case <-time.After(waitTimeout):
			t.Fatal("timed out waiting for the event queued whilst there was no capacity")
		}

		l.Stop()
		mustReturn(t, errCh, nil)
	})
}

func TestListenDropsConnectionWhenCapacityIsLost(t *testing.T) {
	testModes(t, func(t *testing.T, opts ...githubwebhook.Option) {
		relay := newFakeRelay(t)
		capacity := newFakeCapacity(true)
		l, received := newListener(relay.server.URL, withCapacity(capacity, opts)...)

		errCh := runListen(context.Background(), l)
		waitForRequests(t, relay, 1)

		// Losing the capacity to take on work drops the connection rather than
		// holding it open, which is what lets another orchestrator reading the
		// same relay take the events instead.
		capacity.set(false)
		select {
		case <-relay.ended:
		case <-time.After(waitTimeout):
			t.Fatal("the relay connection was not dropped when capacity was lost")
		}
		assertNoRequests(t, relay, 200*time.Millisecond)

		// Capacity returning reconnects, and what the relay queued in the
		// meantime is then delivered.
		relay.deliver("workflow_job", workflowJobBody(t, "queued", 22))
		capacity.set(true)
		select {
		case ev := <-received:
			if got, want := ev.GetWorkflowJob().GetID(), int64(22); got != want {
				t.Errorf("WorkflowJob.ID: got %d, want %d", got, want)
			}
		case <-time.After(waitTimeout):
			t.Fatal("timed out waiting for the event delivered after reconnecting")
		}

		l.Stop()
		mustReturn(t, errCh, nil)
	})
}
