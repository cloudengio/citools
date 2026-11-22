// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"strings"
	"testing"
)

// dispatchConfig runs 'pconn config <args>', returning what it wrote.
func dispatchConfig(t *testing.T, cfgPath string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	configCommands.Output = &out
	t.Cleanup(func() { configCommands.Output = nil })
	cli := newTestCLI(t)
	err := cli.DispatchWithArgs(t.Context(), "pconn", append([]string{"--config=" + cfgPath, "config"}, args...)...)
	return out.String(), err
}

func TestConfigDescribe(t *testing.T) {
	// describe does not need a configuration file.
	out, err := dispatchConfig(t, "nosuch.yaml", "describe")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"port_forwards:", "tmux:", "logging:",
		"forwards:", "groups:", "unlimited_retries:", // port forward fields
		"command:",                                                 // tmux field
		"dial_timeout:", "accept_new_host_keys:", "forward_agent:", // ssh fields
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the description does not contain %q:\n%v", want, out)
		}
	}
	// The fields shared by both sections are described once.
	if got := strings.Count(out, "forward_agent:"); got != 1 {
		t.Errorf("forward_agent is described %d times, want 1", got)
	}
	// groups is only for port forwards.
	tmuxDoc := out[strings.Index(out, "\ntmux:"):]
	if i := strings.Index(tmuxDoc, "Each entry"); i > 0 {
		tmuxDoc = tmuxDoc[:i]
	}
	if strings.Contains(tmuxDoc, "groups:") {
		t.Errorf("the tmux section is documented with groups:\n%v", tmuxDoc)
	}
}

func TestConfigShow(t *testing.T) {
	cfgPath := writeTestConfig(t, t.TempDir(), tmuxTestConfig)
	out, err := dispatchConfig(t, cfgPath, "show")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Configuration file: " + cfgPath, "tmux:", "name: dev", "command: tmux -CC new -A -s main"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not contain %q:\n%v", want, out)
		}
	}
	if strings.Contains(out, "groups") {
		t.Errorf("groups are shown for tmux entries:\n%v", out)
	}
}

func TestConfigShowErrors(t *testing.T) {
	if _, err := dispatchConfig(t, "nosuch.yaml", "show"); err == nil {
		t.Error("expected an error for a missing configuration file")
	}
	// The previous layout is rejected rather than ignored.
	cfgPath := writeTestConfig(t, t.TempDir(), "connections:\n  - name: dev\n    host: example.com\n")
	_, err := dispatchConfig(t, cfgPath, "show")
	if err == nil || !strings.Contains(err.Error(), "connections") {
		t.Errorf("got %v, want an error naming the unknown connections field", err)
	}
	// groups only apply to port forwards.
	cfgPath = writeTestConfig(t, t.TempDir(), "tmux:\n  - name: dev\n    host: example.com\n    groups: [a]\n")
	_, err = dispatchConfig(t, cfgPath, "show")
	if err == nil || !strings.Contains(err.Error(), "groups") {
		t.Errorf("got %v, want an error naming the unknown groups field", err)
	}
}
