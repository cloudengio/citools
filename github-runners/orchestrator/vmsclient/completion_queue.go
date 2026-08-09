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
	logger            *slog.Logger
	successExpiration time.Duration
	failureExpiration time.Duration
	success           *patterns.FIFO[CompletionEvent[T]]
	failure           *patterns.FIFO[CompletionEvent[T]]
}

func NewCompletionQueue[T CompletionEventPayload](ctx context.Context, capacity int, successRetention, errorRetention time.Duration) *CompletionQueue[T] {
	q := &CompletionQueue[T]{
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

func (q *CompletionQueue[T]) closeCQ(ctx context.Context, msg string, cq *patterns.FIFO[CompletionEvent[T]]) error {
	var errs errors.M
	close(cq.In())
	if len(cq.Out()) != 0 {
		for e := range cq.Out() {
			if vm := e.Payload.GetVM(); vm != nil {
				ctxlog.Info(ctx, msg, "vm", vm.ID())
				errs.Append(vm.Delete(ctx))
			}
		}
	}
	cq.Stop(ctx)
	return errs.Err()
}
