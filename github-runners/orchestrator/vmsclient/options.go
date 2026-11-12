// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	basepool "cloudeng.io/vms/vmspool"
)

// DefaultEventBuffer is the number of lifecycle events buffered for a pool
// whilst they are waiting to be counted, see WithEventBuffer. It is the
// tracker's own default, since the counting is the tracker's.
const DefaultEventBuffer = basepool.DefaultStatusBuffer

type options struct {
	baseOpts []basepool.Option
	// outcomes configures the set's completion queue, see
	// WithCompletionOutcomes; none of them means no queue at all.
	outcomes []basepool.CompletionOutcomeOption
	// completion is the queue the set created from outcomes, which it hands to
	// each of its pools, see NewPools. It is not set by an option: the queue
	// belongs to the set rather than to whoever configured it.
	completion *basepool.CompletionQueue
	// eventBuffer is how many of a pool's events may be waiting to be counted,
	// see WithEventBuffer; 0 leaves the tracker to apply its own default.
	eventBuffer int
}

// Option configures a pool, or every pool of a set, at creation.
type Option func(*options)

func optionsFrom(opts []Option) options {
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// WithBaseOptions passes options through to the underlying
// cloudeng.io/vms/vmspool.Pool, for the settings that PoolConfig does not
// carry: where a VM's output is written, for example. Note that the status
// channel is not one of them. It is set here, since the pool's event stream
// is what the counters in a PoolSnapshot are accumulated from, and an option
// that replaced it would silence them.
func WithBaseOptions(opts ...basepool.Option) Option {
	return func(o *options) {
		o.baseOpts = append(o.baseOpts, opts...)
	}
}

// WithCompletionOutcomes gives the set a completion queue, which parks the VMs
// of finished work rather than deleting them at once, and configures the
// outcomes that work is filed under: each has a capacity and a retention
// period of its own, see basepool.WithCompletionOutcome. A set configured with
// no outcomes has no queue at all, which is an application that deletes each
// VM as it finishes with it.
//
// The queue is created with the set, shared by every pool in it and closed
// when the set is, see Pools.CompletionQueue: it is the set that owns it,
// because the VMs of several pools are parked in the one queue and releasing
// them is the last thing to happen. A caller fills and drains it, and must not
// close it.
//
// The pools themselves neither fill the queue nor drain it; that is for the
// application, which knows what finishing means. What a pool does with it is
// hand it to every VM it creates, see basepool.VM.CompletionQueue, so that
// work holding a VM can park it without being given the queue as well, and
// report the VMs of its own that it is holding, see PoolSnapshot.Completing.
func WithCompletionOutcomes(outcomes ...basepool.CompletionOutcomeOption) Option {
	return func(o *options) {
		o.outcomes = append(o.outcomes, outcomes...)
	}
}

// WithEventBuffer sets how many of a pool's lifecycle events are buffered
// whilst they are waiting to be counted; 0 or less is DefaultEventBuffer.
// Events are dropped rather than queued once the buffer is full, so this
// bounds how bursty a pool can be before its counters start to undercount;
// it has no effect on the pool itself.
func WithEventBuffer(size int) Option {
	return func(o *options) {
		o.eventBuffer = size
	}
}
