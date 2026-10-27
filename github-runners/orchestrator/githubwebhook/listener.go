// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubwebhook

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"cloudeng.io/algo/ratecontrol"
	"cloudeng.io/errors"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/sync/errgroup"
	"cloudeng.io/webapi/clients/github"
	"cloudeng.io/webapi/operations"
	gogithub "github.com/google/go-github/v89/github"
)

// retryBackoff is the delay between retries after a transient relay read error.
const retryBackoff = 100 * time.Millisecond

// capacityPollInterval is how often the capacity is re-read whilst it is being
// waited on or held, in addition to its own change signal. It is a safety net:
// a signal that is missed, or a change that is not signalled at all, would
// otherwise leave the listener disconnected from the relay whilst there is
// work it could take on. Listener.pollInterval is what is actually used, so
// that a test can shorten it without disturbing anything else.
const capacityPollInterval = 5 * time.Second

// Capacity reports whether the orchestrator can take on new work. A listener
// configured with one, see WithCapacity, connects to the relay only whilst it
// can, which leaves the events for the time being queued at the relay, or to
// be read by another orchestrator reading the same relay.
type Capacity interface {
	// HasCapacity reports whether work can be taken on now. It is called
	// often and from the listener's own goroutines, so it must be cheap and
	// safe for concurrent use.
	HasCapacity() bool
	// Changed returns a channel that is signalled when the answer HasCapacity
	// gives may have changed. The channel is read but never closed, signals on
	// it may be coalesced, and it may be nil for an implementation that has no
	// such signal: the listener also re-reads the capacity periodically, so a
	// signal makes it react promptly rather than at all.
	Changed() <-chan struct{}
}

// Handler processes a single workflow_job webhook event. Any error it returns
// is logged; because handlers run in their own goroutine there is no caller to
// return it to.
type Handler func(context.Context, *gogithub.WorkflowJobEvent) error

// Option configures a Listener.
type Option func(*Listener)

// WithStreaming configures whether the listener uses the streaming webhook
// API (stream=1) instead of polling hanging GET requests.
func WithStreaming(streaming bool) Option {
	return func(l *Listener) {
		l.streaming = streaming
	}
}

// WithCapacity configures the listener to connect to the relay only whilst c
// reports that work can be taken on. Without it the listener stays connected
// regardless of whether anything can be done with the events it reads.
//
// The relay queues what it receives whilst nothing is reading it, so the
// events arrive once there is capacity again. They are not kept indefinitely:
// a relay drops the oldest delivery when its queue is full and, if it is
// configured to, expires them, so a listener that stays away for long enough
// can still miss events.
func WithCapacity(c Capacity) Option {
	return func(l *Listener) {
		l.capacity = c
	}
}

type Listener struct {
	relayURL     string
	doneCh       chan struct{}
	stopOnce     sync.Once
	handler      Handler
	streaming    bool
	capacity     Capacity      // nil when the connection is not gated on capacity
	pollInterval time.Duration // capacityPollInterval, shortened by tests
}

func New(relayURL string, handler Handler, opts ...Option) *Listener {
	l := &Listener{
		relayURL:     relayURL,
		doneCh:       make(chan struct{}),
		handler:      handler,
		pollInterval: capacityPollInterval,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// DoneCh returns a channel that is closed when the listener is stopped.
// It can be used to wait for the listener to stop.
func (l *Listener) DoneCh() <-chan struct{} {
	return l.doneCh
}

// hasCapacity reports whether work can be taken on. A listener with no
// capacity configured always can.
func (l *Listener) hasCapacity() bool {
	return l.capacity == nil || l.capacity.HasCapacity()
}

// capacityChanged returns the capacity's change signal, or nil when there is
// no capacity to watch, which blocks the select it is used in rather than
// spinning it.
func (l *Listener) capacityChanged() <-chan struct{} {
	if l.capacity == nil {
		return nil
	}
	return l.capacity.Changed()
}

// capacityPoll returns a channel that polls the capacity, as a safety net for
// a change that is not signalled, and the function that stops it. Both are
// usable when there is no capacity to watch: the channel is then nil.
func (l *Listener) capacityPoll() (<-chan time.Time, func()) {
	if l.capacity == nil {
		return nil, func() {}
	}
	interval := l.pollInterval
	if interval <= 0 {
		interval = capacityPollInterval
	}
	t := time.NewTicker(interval)
	return t.C, t.Stop
}

// waitForCapacity blocks until work can be taken on, reporting whether it is.
// It returns false when the listener was stopped or ctx was cancelled whilst
// waiting, in which case the caller returns rather than connecting.
func (l *Listener) waitForCapacity(ctx context.Context) bool {
	if l.hasCapacity() {
		return true
	}
	ctxlog.Info(ctx, "Listen: not reading from the relay: no capacity for new work", "relay_url", l.relayURL)
	changed := l.capacityChanged()
	poll, stopPoll := l.capacityPoll()
	defer stopPoll()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-l.doneCh:
			return false
		case <-changed:
		case <-poll:
		}
		if l.hasCapacity() {
			ctxlog.Info(ctx, "Listen: capacity for new work, reading from the relay", "relay_url", l.relayURL)
			return true
		}
	}
}

// Listen starts listening for workflow_job events from the relay. It will
// block until the context is cancelled, the listener is stopped, or the relay
// closes cleanly. For each workflow_job event received, the handler function
// is called in a separate goroutine.
//
// A listener configured with a Capacity, see WithCapacity, reads from the
// relay only whilst there is work it can take on: it drops its connection when
// there is not, and re-establishes it when there is again.
func (l *Listener) Listen(ctx context.Context, opts []operations.Option) error {
	if l.streaming {
		return l.listenStream(ctx, opts)
	}
	return l.listenPoll(ctx, opts)
}

func (l *Listener) listenStream(ctx context.Context, opts []operations.Option) error {
	var g errgroup.T
	defer func() { _ = g.Wait() }()

	exponentialBackoff := ratecontrol.NewExponentialBackoff(time.Second, 100)
	spinBackoff := ratecontrol.NewBackoffOnSpin(100, time.Millisecond*500, exponentialBackoff)

	for {
		select {
		case <-ctx.Done():
			var errs errors.M
			errs.Append(g.Wait())
			errs.Append(ctx.Err())
			return errs.Err()
		case <-l.doneCh:
			return g.Wait()
		default:
		}

		if !l.waitForCapacity(ctx) {
			var errs errors.M
			errs.Append(g.Wait())
			errs.Append(ctx.Err())
			return errs.Err()
		}

		done, err := spinBackoff.Wait(ctx, nil)
		if done || err != nil {
			var errs errors.M
			errs.Append(g.Wait())
			errs.Append(ctx.Err())
			return errs.Err()
		}

		stream, streamCancel, err := l.connectStream(ctx, opts)
		if err != nil {
			if ctx.Err() != nil {
				var errs errors.M
				errs.Append(g.Wait())
				errs.Append(ctx.Err())
				return errs.Err()
			}
			select {
			case <-l.doneCh:
				return g.Wait()
			default:
			}
			ctxlog.Warn(ctx, "Listen: stream connect failed, retrying", "relay_url", l.relayURL, "err", err)
			select {
			case <-ctx.Done():
				var errs errors.M
				errs.Append(g.Wait())
				errs.Append(ctx.Err())
				return errs.Err()
			case <-l.doneCh:
				return g.Wait()
			case <-time.After(retryBackoff):
			}
			continue
		}

		relayClosed, streamErr := l.consumeStream(ctx, stream, &g)
		_ = stream.Close()
		streamCancel()

		if relayClosed {
			ctxlog.Info(ctx, "Listen: relay closed", "relay_url", l.relayURL)
			var errs errors.M
			errs.Append(g.Wait())
			if ctx.Err() != nil {
				errs.Append(ctx.Err())
			}
			return errs.Err()
		}

		if ctx.Err() != nil {
			var errs errors.M
			errs.Append(g.Wait())
			errs.Append(ctx.Err())
			return errs.Err()
		}

		select {
		case <-l.doneCh:
			return g.Wait()
		default:
		}

		if streamErr != nil {
			if !l.hasCapacity() {
				// The stream was ended by watchStream because no more work can
				// be taken on, so this is not a failure and must not be backed
				// off: the next iteration waits for capacity before
				// reconnecting.
				ctxlog.Info(ctx, "Listen: dropped the relay connection: no capacity for new work", "relay_url", l.relayURL, "err", streamErr)
				continue
			}
			ctxlog.Warn(ctx, "Listen: stream read failed, retrying", "relay_url", l.relayURL, "err", streamErr)
			select {
			case <-ctx.Done():
				var errs errors.M
				errs.Append(g.Wait())
				errs.Append(ctx.Err())
				return errs.Err()
			case <-l.doneCh:
				return g.Wait()
			case <-time.After(retryBackoff):
			}
		}
	}
}

func (l *Listener) connectStream(ctx context.Context, opts []operations.Option) (*github.WebhookStream, context.CancelFunc, error) {
	streamCtx, streamCancel := context.WithCancel(ctx)
	stopStream := make(chan struct{})
	go l.watchStream(streamCtx, stopStream, streamCancel)
	stream, err := github.StreamWebhookEvents(streamCtx, l.relayURL, opts...)
	if err != nil {
		close(stopStream)
		streamCancel()
		return nil, nil, err
	}
	cancel := func() {
		close(stopStream)
		streamCancel()
	}
	return stream, cancel, nil
}

// watchStream cancels a stream's context when the listener is stopped or the
// capacity to take on work is lost, which ends the read in consumeStream and
// so drops the connection to the relay. It returns when the stream ends, ie.
// when stop is closed or streamCtx is cancelled.
func (l *Listener) watchStream(streamCtx context.Context, stop <-chan struct{}, cancel context.CancelFunc) {
	changed := l.capacityChanged()
	poll, stopPoll := l.capacityPoll()
	defer stopPoll()
	for {
		select {
		case <-streamCtx.Done():
			return
		case <-stop:
			return
		case <-l.doneCh:
			cancel()
			return
		case <-changed:
		case <-poll:
		}
		if !l.hasCapacity() {
			cancel()
			return
		}
	}
}

func (l *Listener) consumeStream(ctx context.Context, stream *github.WebhookStream, g *errgroup.T) (relayClosed bool, err error) {
	for {
		eventType, payload, err := stream.Next()
		if err != nil {
			if errors.Is(err, github.ErrRelayClosed) {
				return true, nil
			}
			return false, err
		}
		if eventType != "" && eventType != "workflow_job" {
			ctxlog.Info(ctx, "consumeStream: skipping unexpected event", "event_type", eventType)
			continue
		}
		var event gogithub.WorkflowJobEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			ctxlog.Error(ctx, "consumeStream: failed to decode workflow_job event", "err", err)
			continue
		}
		ev := event
		g.GoContext(ctx, func() error {
			if err := l.handler(ctx, &ev); err != nil {
				ctxlog.Error(ctx, "workflow_job handler failed", "err", err)
				return err
			}
			return nil
		})
	}
}

func (l *Listener) listenPoll(ctx context.Context, opts []operations.Option) error {
	var g errgroup.T
	defer func() { _ = g.Wait() }()

	for {
		if !l.waitForCapacity(ctx) {
			var errs errors.M
			errs.Append(g.Wait())
			errs.Append(ctx.Err())
			return errs.Err()
		}
		wctx, cancel := context.WithCancel(ctx)
		eventCh := make(chan *gogithub.WorkflowJobEvent, 1)
		go l.waitForEvent(wctx, eventCh, opts)
		event, resume := l.awaitEvent(ctx, eventCh)
		cancel()
		if !resume {
			var errs errors.M
			errs.Append(g.Wait())
			errs.Append(ctx.Err())
			return errs.Err()
		}
		if event == nil {
			// The read was abandoned, but the reader may have delivered an
			// event as it was cancelled: the relay treats a delivery it has
			// written as read, so one that did arrive is handled rather than
			// dropped.
			event = drainEvent(eventCh)
		}
		if event == nil {
			continue
		}
		g.GoContext(ctx, func() error {
			if err := l.handler(ctx, event); err != nil {
				ctxlog.Error(ctx, "workflow_job handler failed", "err", err)
				return err
			}
			return nil
		})
	}
}

// awaitEvent waits for the reader to deliver an event. resume is false when
// the reader is finished with, ie. the relay closed cleanly, the listener was
// stopped, or ctx was cancelled, and the caller returns; it is true when
// reading should carry on, with the event that was read, or with none when the
// capacity to take on work was lost and the read was abandoned.
func (l *Listener) awaitEvent(ctx context.Context, eventCh <-chan *gogithub.WorkflowJobEvent) (event *gogithub.WorkflowJobEvent, resume bool) {
	changed := l.capacityChanged()
	poll, stopPoll := l.capacityPoll()
	defer stopPoll()
	for {
		select {
		case event, ok := <-eventCh:
			return event, ok
		case <-ctx.Done():
			return nil, false
		case <-l.doneCh:
			return nil, false
		case <-changed:
		case <-poll:
		}
		if !l.hasCapacity() {
			ctxlog.Info(ctx, "Listen: dropped the relay connection: no capacity for new work", "relay_url", l.relayURL)
			return nil, true
		}
	}
}

// drainEvent returns an event that is already waiting on eventCh, or nil if
// there is none.
func drainEvent(eventCh <-chan *gogithub.WorkflowJobEvent) *gogithub.WorkflowJobEvent {
	select {
	case event := <-eventCh:
		return event
	default:
		return nil
	}
}

// waitForEvent performs hanging reads against the relay until it receives a
// workflow_job event, which it sends on eventCh, which must be buffered.
// Non-workflow_job events are skipped and transient errors are retried; it
// returns without sending only when ctx is cancelled.
func (l *Listener) waitForEvent(ctx context.Context, eventCh chan<- *gogithub.WorkflowJobEvent, opts []operations.Option) {
	exponentialBackoff := ratecontrol.NewExponentialBackoff(time.Second, 100)
	spinBackoff := ratecontrol.NewBackoffOnSpin(100, time.Millisecond*500, exponentialBackoff)
	defer close(eventCh)
	for {
		done, err := spinBackoff.Wait(ctx, nil)
		if done || err != nil {
			return
		}
		event, err := github.ReadWorkflowJobEvent(ctx, l.relayURL, opts...)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, github.ErrRelayClosed) {
				// The relay closed cleanly; stop reading so that Listen observes
				// the closed eventCh and returns nil.
				ctxlog.Info(ctx, "waitForEvent: relay closed", "relay_url", l.relayURL)
				return
			}
			ctxlog.Warn(ctx, "waitForEvent: read failed, retrying", "relay_url", l.relayURL, "err", err)
			// Back off briefly before retrying, but wake immediately if the
			// context is cancelled so shutdown is not delayed.
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryBackoff):
			}
			continue
		}
		// eventCh is buffered and carries one event, so this never blocks and
		// does not need to be abandoned when ctx is cancelled. An event that
		// has been read must not be dropped: the relay treats a delivery it
		// has written as read, so it would not be sent again.
		eventCh <- &event
		return
	}
}

// Stop stops the listener. It is safe to call Stop multiple times.
func (l *Listener) Stop() {
	l.stopOnce.Do(func() { close(l.doneCh) })
}
