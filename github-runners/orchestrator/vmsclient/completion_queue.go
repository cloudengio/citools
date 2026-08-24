// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"log/slog"
	"time"

	"cloudeng.io/errors"
	"cloudeng.io/logging/ctxlog"
	"cloudeng.io/sync/patterns"
	"cloudeng.io/vms/vmspool"
)

type CompletionEventPayload interface {
	GetVM() *vmspool.VM
	GetLogger(*slog.Logger) *slog.Logger
}

type CompletionEvent[T CompletionEventPayload] struct {
	expirationTime time.Time
	Err            error
	Payload        T
}

func (e *CompletionEvent[T]) Expiration() time.Time {
	return e.expirationTime
}

type CompletionQueue[T CompletionEventPayload] struct {
	// ctx is the context the FIFOs were created with, and hence governs their
	// goroutines. Close consults it to tell a live queue, which will flush and
	// close its output channel, from one whose goroutines have already exited.
	ctx               context.Context
	logger            *slog.Logger
	successExpiration time.Duration
	failureExpiration time.Duration
	success           *patterns.FIFO[CompletionEvent[T]]
	failure           *patterns.FIFO[CompletionEvent[T]]
}

func NewCompletionQueue[T CompletionEventPayload](ctx context.Context, capacity int, successRetention, errorRetention time.Duration) *CompletionQueue[T] {
	q := &CompletionQueue[T]{
		ctx:               ctx,
		logger:            ctxlog.Logger(ctx),
		successExpiration: successRetention,
		failureExpiration: errorRetention,
	}
	q.success = patterns.NewFIFO(ctx, capacity,
		patterns.WithPeriodicScan(time.Second, q.expiration))
	q.failure = patterns.NewFIFO(ctx, capacity,
		patterns.WithPeriodicScan(time.Second, q.expiration))
	return q
}

func (q *CompletionQueue[T]) PushSuccess(event CompletionEvent[T]) {
	event.expirationTime = time.Now().Add(q.successExpiration)
	q.success.In() <- event
}

func (q *CompletionQueue[T]) PushFailure(event CompletionEvent[T], err error) {
	event.Err = err
	event.expirationTime = time.Now().Add(q.failureExpiration)
	q.failure.In() <- event
}

func (q *CompletionQueue[T]) expiration(e CompletionEvent[T]) bool {
	if time.Now().After(e.expirationTime) {
		logger := e.Payload.GetLogger(q.logger)
		if e.Err != nil {
			logger.Error("completion event expired for failed job", "error", e.Err)
		} else {
			logger.Info("completion event expired for successful job")
		}
		if vm := e.Payload.GetVM(); vm != nil {
			if err := vm.Delete(context.Background()); err != nil {
				logger.Error("failed to delete VM", "deletion_err", err)
			}
		}
		return true
	}
	return false
}

func (q *CompletionQueue[T]) Success() <-chan CompletionEvent[T] {
	return q.success.Out()
}

func (q *CompletionQueue[T]) Failure() <-chan CompletionEvent[T] {
	return q.failure.Out()
}

func (q *CompletionQueue[T]) Close(ctx context.Context) error {
	var errs errors.M
	errs.Append(q.closeCQ(ctx, "releasing vm from success queue", q.success))
	errs.Append(q.closeCQ(ctx, "releasing vm from failure queue", q.failure))
	return errs.Err()
}

// drainTimeout bounds how long closeCQ waits for a live queue to deliver its
// next event before giving up on it.
const drainTimeout = 5 * time.Second

// releaseVM deletes the VM an event was holding, if any.
func (q *CompletionQueue[T]) releaseVM(ctx context.Context, msg string, e CompletionEvent[T], errs *errors.M) {
	vm := e.Payload.GetVM()
	if vm == nil {
		return
	}
	ctxlog.Info(ctx, msg, "vm", vm.ID())
	errs.Append(vm.Delete(ctx))
}

// drainNonBlocking drains whatever events are already buffered in the FIFO's
// output channel without waiting for further delivery.
func (q *CompletionQueue[T]) drainNonBlocking(ctx context.Context, msg string, cq *patterns.FIFO[CompletionEvent[T]], errs *errors.M) error {
	for {
		select {
		case e, ok := <-cq.Out():
			if !ok {
				cq.Stop(ctx)
				return errs.Err()
			}
			q.releaseVM(ctx, msg, e, errs)
		default:
			cq.Stop(ctx)
			return errs.Err()
		}
	}
}

func (q *CompletionQueue[T]) closeCQ(ctx context.Context, msg string, cq *patterns.FIFO[CompletionEvent[T]]) error {
	var errs errors.M
	close(cq.In())
	if q.ctx.Err() != nil {
		// The queue's context has been cancelled, as it is on interrupt, so the
		// FIFO's goroutine has already returned without closing Out. Only what
		// it delivered before exiting can be drained; a blocking receive would
		// never return, which is what used to hang shutdown.
		return q.drainNonBlocking(ctx, msg, cq, &errs)
	}
	// The queue is live, so closing In makes the FIFO flush its buffer and then
	// close Out. Wait for that, bounded so that a wedged FIFO cannot stall
	// shutdown either.
	timer := time.NewTimer(drainTimeout)
	defer timer.Stop()
	for {
		select {
		case e, ok := <-cq.Out():
			if !ok {
				cq.Stop(ctx)
				return errs.Err()
			}
			q.releaseVM(ctx, msg, e, &errs)
			// Deleting a VM takes time, so the timeout bounds the wait for each
			// event rather than the drain as a whole.
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(drainTimeout)
		case <-q.ctx.Done():
			// The queue's context was cancelled mid-drain; drain whatever was
			// delivered before the FIFO exited and return.
			return q.drainNonBlocking(ctx, msg, cq, &errs)
		case <-ctx.Done():
			cq.Stop(ctx)
			return errs.Err()
		case <-timer.C:
			ctxlog.Warn(ctx, "gave up draining the completion queue", "queue", msg, "timeout", drainTimeout)
			cq.Stop(ctx)
			return errs.Err()
		}
	}
}
