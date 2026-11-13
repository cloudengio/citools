// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package internal_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudengio/citools/runners/macos/orchestrator/internal"
)

// newTimestampWriter returns a writer whose clock advances by a second on each
// line, so that the test can tell which timestamp belongs to which line.
func newTimestampWriter(out *strings.Builder) *internal.TimestampWriter {
	var calls int
	now := func() time.Time {
		calls++
		return time.Date(2026, 11, 12, 10, 0, calls-1, 0, time.UTC)
	}
	return internal.NewTimestampWriter(out,
		internal.WithTimestampClock(now),
		internal.WithTimestampLayout(time.RFC3339))
}

func TestTimestampWriterLines(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input []string
		want  string
	}{
		{"one line", []string{"error: nope\n"}, "2026-11-12T10:00:00Z error: nope\n"},
		// A multi-line message, such as a configuration file that failed to
		// parse, is timestamped on every line and not just the first.
		{"one write, three lines",
			[]string{"error: bad config\n  line 60: no such field\n  line 61: no such field\n"},
			"2026-11-12T10:00:00Z error: bad config\n" +
				"2026-11-12T10:00:01Z   line 60: no such field\n" +
				"2026-11-12T10:00:02Z   line 61: no such field\n"},
		// A line that arrives in pieces is timestamped once, when it is
		// completed, rather than at each write.
		{"one line, three writes",
			[]string{"error: ", "nope", "\n"},
			"2026-11-12T10:00:00Z error: nope\n"},
		// A blank line keeps its role of separating the parts of a message.
		{"blank line", []string{"first\n\nsecond\n"},
			"2026-11-12T10:00:00Z first\n\n2026-11-12T10:00:01Z second\n"},
	} {
		var out strings.Builder
		w := newTimestampWriter(&out)
		for _, s := range tc.input {
			n, err := w.Write([]byte(s))
			if err != nil {
				t.Fatalf("%v: Write: %v", tc.name, err)
			}
			if n != len(s) {
				t.Errorf("%v: Write returned %v, want %v", tc.name, n, len(s))
			}
		}
		if got := out.String(); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestTimestampWriterFlush verifies that a message written without a trailing
// newline is held back until Flush, which is what the orchestrator calls before
// exiting, and that Flush is harmless when nothing is held back.
func TestTimestampWriterFlush(t *testing.T) {
	var out strings.Builder
	w := newTimestampWriter(&out)
	if _, err := w.Write([]byte("press Enter to continue")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := out.String(); got != "" {
		t.Errorf("an unterminated line was written early: %q", got)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got, want := out.String(), "2026-11-12T10:00:00Z press Enter to continue\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	if got, want := out.String(), "2026-11-12T10:00:00Z press Enter to continue\n"; got != want {
		t.Errorf("a second Flush wrote again: got %q, want %q", got, want)
	}
}

// TestTimestampWriterConcurrent verifies that concurrent writers, which the
// orchestrator has whenever more than one goroutine reports a problem, neither
// race nor interleave within a line.
func TestTimestampWriterConcurrent(t *testing.T) {
	var out strings.Builder
	w := internal.NewTimestampWriter(&out)
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			fmt.Fprintf(w, "line %v\n", i) //nolint:errcheck
		})
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if got, want := len(lines), 10; got != want {
		t.Fatalf("got %v lines, want %v: %q", got, want, out.String())
	}
	seen := map[string]bool{}
	for _, l := range lines {
		ts, msg, ok := strings.Cut(l, " ")
		if !ok {
			t.Fatalf("line %q has no timestamp", l)
		}
		if _, err := time.Parse(time.RFC3339, ts); err != nil {
			t.Errorf("line %q: %v", l, err)
		}
		seen[msg] = true
	}
	for i := range 10 {
		if msg := fmt.Sprintf("line %v", i); !seen[msg] {
			t.Errorf("%q was not written", msg)
		}
	}
}
