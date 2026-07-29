// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package githubwebhook

import (
	"context"
	"errors"
	"time"

	"cloudeng.io/algo/ratecontrol"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/webapi/clients/github"
	"cloudeng.io/webapi/operations"
	gogithub "github.com/google/go-github/v89/github"
)

// Handler processes a single workflow_job webhook event. Any error it returns
// is logged; because handlers run in their own goroutine there is no caller to
// return it to.
type Handler func(context.Context, *gogithub.WorkflowJobEvent) error

type Listener struct {
	relayURL string
	doneCh   chan struct{}
	handler  Handler
}

func New(relayURL string, handler Handler) *Listener {
	return &Listener{
		relayURL: relayURL,
		doneCh:   make(chan struct{}),
		handler:  handler,
	}
}

// DoneCh returns a channel that is closed when the listener is stopped.
// It can be used to wait for the listener to stop.
func (l *Listener) DoneCh() <-chan struct{} {
	return l.doneCh
}

// Listen starts listening for workflow_job events from the relay. It will
// block until the context is cancelled or the listener is stopped. For each
// workflow_run event received, the handler function is called in a separate
// goroutine.
func (l *Listener) Listen(ctx context.Context, opts []operations.Option) error {
	for {
		wctx, cancel := context.WithCancel(ctx)
		eventCh := make(chan *gogithub.WorkflowJobEvent, 1)
		go l.waitForEvent(wctx, eventCh, opts)
		select {
		case event := <-eventCh:
			cancel()
			if event == nil {
				return nil
			}
			go func() {
				if err := l.handler(ctx, event); err != nil {
					ctxlog.Error(ctx, "workflow_job handler failed", "err", err)
				}
			}()
		case <-ctx.Done():
			cancel()
			return ctx.Err()
		case <-l.doneCh:
			cancel()
			return nil
		}
	}
}

// waitForEvent performs hanging reads against the relay until it receives a
// workflow_run event, which it sends on eventCh. Non-workflow_run events are
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
			ctxlog.Warn(ctx, "waitForEvent: read failed, retrying", "relay_url", l.relayURL,
				"relay_closed", errors.Is(err, github.ErrRelayClosed), "err", err)
			time.Sleep(time.Second) // Need a better backoff/retry strategy here
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
	close(l.doneCh)
}
