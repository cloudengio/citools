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

type Listener struct {
	relayURL  string
	doneCh    chan struct{}
	stopOnce  sync.Once
	handler   Handler
	streaming bool
}

func New(relayURL string, handler Handler, opts ...Option) *Listener {
	l := &Listener{
		relayURL: relayURL,
		doneCh:   make(chan struct{}),
		handler:  handler,
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

// Listen starts listening for workflow_job events from the relay. It will
// block until the context is cancelled, the listener is stopped, or the relay
// closes cleanly. For each workflow_job event received, the handler function
// is called in a separate goroutine.
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
	go func() {
		select {
		case <-streamCtx.Done():
		case <-l.doneCh:
			streamCancel()
		case <-stopStream:
		}
	}()
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
		wctx, cancel := context.WithCancel(ctx)
		eventCh := make(chan *gogithub.WorkflowJobEvent, 1)
		go l.waitForEvent(wctx, eventCh, opts)
		select {
		case event, ok := <-eventCh:
			cancel()
			if !ok {
				// channel closed, return any errors
				var errs errors.M
				errs.Append(g.Wait())
				errs.Append(ctx.Err())
				return errs.Err()
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
		case <-ctx.Done():
			cancel()
			var errs errors.M
			errs.Append(g.Wait())
			errs.Append(ctx.Err())
			return errs.Err()
		case <-l.doneCh:
			cancel()
			return g.Wait()
		}
	}
}

// waitForEvent performs hanging reads against the relay until it receives a
// workflow_job event, which it sends on eventCh. Non-workflow_job events are
// skipped and transient errors are retried; it returns without sending only
// when ctx is cancelled.
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
		select {
		case eventCh <- &event:
		case <-ctx.Done():
		}
		return
	}
}

// Stop stops the listener. It is safe to call Stop multiple times.
func (l *Listener) Stop() {
	l.stopOnce.Do(func() { close(l.doneCh) })
}
