// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package internal

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// TimestampWriter wraps a writer so that every line written to it is prefixed
// with the time at which the line was completed. It is used for the
// orchestrator's standard error, which the login service captures in its
// .err.log and which is read long after the fact, so that a multi-line message
// says when it was written on each of its lines rather than only the first.
type TimestampWriter struct {
	mu      sync.Mutex
	out     io.Writer
	partial []byte // the line being accumulated, without its newline
	now     func() time.Time
	layout  string
}

// TimestampOption configures a TimestampWriter.
type TimestampOption func(*TimestampWriter)

// WithTimestampLayout sets the time layout used for the prefix, which defaults
// to time.RFC3339.
func WithTimestampLayout(layout string) TimestampOption {
	return func(w *TimestampWriter) { w.layout = layout }
}

// WithTimestampClock sets the function that supplies the time, which defaults
// to time.Now. It is intended for tests.
func WithTimestampClock(now func() time.Time) TimestampOption {
	return func(w *TimestampWriter) { w.now = now }
}

// NewTimestampWriter returns a TimestampWriter that writes to out.
func NewTimestampWriter(out io.Writer, opts ...TimestampOption) *TimestampWriter {
	w := &TimestampWriter{out: out, now: time.Now, layout: time.RFC3339}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Write implements io.Writer. Every complete line is written to the underlying
// writer prefixed with the time and a single space; a blank line is left bare,
// so that the blank lines separating the parts of a message still read as such.
// A trailing partial line is held back until the write that completes it, or
// until Flush, since the timestamp is of no use half way along a line.
func (w *TimestampWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial = append(w.partial, p...)
	var out []byte
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		out = w.appendLine(out, w.partial[:i])
		w.partial = w.partial[i+1:]
	}
	if len(out) == 0 {
		return len(p), nil
	}
	// The lines of one Write are emitted as a single write so that they are
	// not interleaved with another goroutine's.
	if _, err := w.out.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush writes any partial line that is still held back, terminating it with a
// newline. It is called before exiting, since a message written without a
// trailing newline would otherwise be lost.
func (w *TimestampWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.partial) == 0 {
		return nil
	}
	out := w.appendLine(nil, w.partial)
	w.partial = w.partial[:0]
	_, err := w.out.Write(out)
	return err
}

// appendLine appends line, timestamped and newline terminated, to out.
func (w *TimestampWriter) appendLine(out, line []byte) []byte {
	if len(line) > 0 {
		out = w.now().AppendFormat(out, w.layout)
		out = append(out, ' ')
		out = append(out, line...)
	}
	return append(out, '\n')
}
