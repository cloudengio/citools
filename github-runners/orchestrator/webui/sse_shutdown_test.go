// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package webui

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSSEStreamStopsOnBaseContextCancel verifies that an open /events SSE
// connection terminates when the server's base context is cancelled. The run
// command wires http.Server.BaseContext to the app context, so this is what lets
// Ctrl-C shut the server down promptly instead of Shutdown blocking forever on a
// never-idle SSE connection.
func TestSSEStreamStopsOnBaseContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ts := httptest.NewUnstartedServer(NewServer(newFakeBackend()).Handler())
	ts.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	ts.Start()
	defer ts.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(ts.URL + BasePath + "/events") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Read the initial hello frame to confirm the stream is live.
	buf := make([]byte, 256)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatalf("reading first frame: %v", err)
	}

	// Simulate Ctrl-C: cancelling the base context must cancel the in-flight SSE
	// handler, ending the stream.
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, resp.Body)
		done <- err
	}()
	select {
	case <-done:
		// Stream ended (EOF or connection closed) — success.
	case <-time.After(3 * time.Second):
		t.Fatal("SSE stream did not terminate within 3s of base-context cancel")
	}
}
