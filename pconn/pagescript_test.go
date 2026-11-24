// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// pageClick is one click on the page, as testdata/pagescript.js makes it.
type pageClick struct {
	Kind    string   `json:"kind"`
	Target  string   `json:"target"`
	Members []string `json:"members,omitempty"`
	Open    string   `json:"open,omitempty"`
}

// pageScriptReport is what testdata/pagescript.js says the page's script did.
type pageScriptReport struct {
	Opened []struct {
		URL string `json:"url"`
	} `json:"opened"`
	Blurred int `json:"blurred"`
	Focused int `json:"focused"`
	Fetches []struct {
		URL      string `json:"url"`
		Method   string `json:"method"`
		FromPage bool   `json:"fromPage"`
	} `json:"fetches"`
	Messages map[string]struct {
		Text string `json:"text"`
		Bad  bool   `json:"bad"`
	} `json:"messages"`
	Errors []string `json:"errors"`
}

// fetchIndex returns the position of the first request for method and url
// that came from the page, or -1 if there was none.
func (r pageScriptReport) fetchIndex(method, url string) int {
	for i, f := range r.Fetches {
		if f.Method == method && f.URL == url && f.FromPage {
			return i
		}
	}
	return -1
}

func (r pageScriptReport) fetched(method, url string) bool {
	return r.fetchIndex(method, url) >= 0
}

func (r pageScriptReport) opened() []string {
	var urls []string
	for _, o := range r.Opened {
		urls = append(urls, o.URL)
	}
	return urls
}

var pageScriptRE = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// runPageScript renders the page that 'pconn serve' serves, runs its script
// with node against a stand-in for the browser, makes the clicks in turn and
// returns what the script did.
func runPageScript(t *testing.T, env []string, clicks ...pageClick) pageScriptReport {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed, so the page's script cannot be run")
	}

	sup, _ := newTestSupervisor(t, webTestConfig)
	s := &webServer{src: sup, logger: slog.New(slog.DiscardHandler), logFile: "/var/log/pconn.log"}
	_, body := get(t, s, "/")
	m := pageScriptRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("the page has no script:\n%v", body)
	}
	scriptPath := filepath.Join(t.TempDir(), "page.js")
	if err := os.WriteFile(scriptPath, []byte(m[1]), 0600); err != nil {
		t.Fatal(err)
	}
	clicksJSON, err := json.Marshal(clicks)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), node, "testdata/pagescript.js", scriptPath, string(clicksJSON)) //nolint:gosec // The test's own script.
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("running the page's script: %v: %s", err, stderr)
	}
	var report pageScriptReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(report.Errors) > 0 {
		// A button whose handler fails does nothing at all, which is the
		// failure this exists to catch.
		t.Fatalf("the page's script failed: %v", strings.Join(report.Errors, "; "))
	}
	return report
}

func startClick(target, open string, members ...string) pageClick {
	return pageClick{Kind: "start", Target: target, Members: members, Open: open}
}

func stopClick(target string, members ...string) pageClick {
	return pageClick{Kind: "stop", Target: target, Members: members}
}

// TestPageScriptMembers covers the members that the page gives each button,
// which is what decides which starts and stops can run at once.
func TestPageScriptMembers(t *testing.T) {
	sup, _ := newTestSupervisor(t, webTestConfig)
	s := &webServer{src: sup, logger: slog.New(slog.DiscardHandler)}
	_, body := get(t, s, "/")
	for _, want := range []string{
		// A group is its connections, and a connection is itself.
		`data-start="dev"`,
		`data-members="[&#34;dev-web&#34;,&#34;db&#34;]"`,
		`data-start="db" data-open="http://127.0.0.1:5432" data-members="[&#34;db&#34;]"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not contain %q", want)
		}
	}
}

// TestPageScriptStart covers clicking a start button: the connections are
// started, their pages are opened at their urls once the connections are up,
// and the focus is given back to the page.
func TestPageScriptStart(t *testing.T) {
	const urls = "http://127.0.0.1:8080 http://127.0.0.1:8443"
	r := runPageScript(t, nil, startClick("dev", urls, "dev-web", "db"))

	if !r.fetched("POST", "/api/start?target=dev") {
		t.Errorf("the group was not started from the page: %+v", r.Fetches)
	}
	if !r.fetched("GET", "/api/established?target=dev") {
		t.Errorf("the page did not wait for the group to be established: %+v", r.Fetches)
	}
	// A tab for each page, at its own url, opened once the connections are
	// up: nothing is opened while the group is being started.
	if got, want := r.opened(), strings.Fields(urls); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if r.Focused == 0 || r.Blurred == 0 {
		t.Errorf("the focus was not given back to the page: focused %v, blurred %v", r.Focused, r.Blurred)
	}
	if got := r.Messages["dev"].Text; got != "dev is up" {
		t.Errorf("got %q, want the group reported as up", got)
	}
}

// TestPageScriptStartNoTabs covers a connection with no web forwards, which
// is started without opening anything, the log included.
func TestPageScriptStartNoTabs(t *testing.T) {
	r := runPageScript(t, nil, startClick("cache", ""))
	if len(r.Opened) != 0 {
		t.Errorf("starting opened tabs: %+v", r.Opened)
	}
	if !r.fetched("POST", "/api/start?target=cache") {
		t.Errorf("the connection was not started: %+v", r.Fetches)
	}
}

// TestPageScriptStartFails covers a connection that cannot be started: the
// page says why, and opens nothing for it.
func TestPageScriptStartFails(t *testing.T) {
	r := runPageScript(t, []string{"PCONN_FAIL_START=dev"}, startClick("dev", "http://127.0.0.1:8080", "dev-web", "db"))
	if len(r.Opened) != 0 {
		t.Errorf("tabs were opened for a connection that could not be started: %+v", r.Opened)
	}
	msg := r.Messages["dev"]
	if !msg.Bad || !strings.Contains(msg.Text, "could not start dev") || !strings.Contains(msg.Text, "already forwarded") {
		t.Errorf("got %+v, want the reason the start failed", msg)
	}
}

// TestPageScriptStop covers clicking a stop button, which opens nothing.
func TestPageScriptStop(t *testing.T) {
	r := runPageScript(t, nil, stopClick("dev", "dev-web", "db"))
	if !r.fetched("POST", "/api/stop?target=dev") {
		t.Errorf("the group was not stopped: %+v", r.Fetches)
	}
	if len(r.Opened) != 0 {
		t.Errorf("stopping opened tabs: %+v", r.Opened)
	}
}

// TestPageScriptIndependentGroups covers starting a group while another is
// still being started, and failing to come up: the two have no connection in
// common, so each runs independently of the other.
func TestPageScriptIndependentGroups(t *testing.T) {
	r := runPageScript(t, []string{"PCONN_PENDING=dev"},
		startClick("dev", "http://127.0.0.1:8080", "dev-web", "db"),
		startClick("cache", "http://127.0.0.1:6379", "cache"),
	)
	if !r.fetched("POST", "/api/start?target=cache") {
		t.Fatalf("the second group was not started while the first was starting: %+v", r.Fetches)
	}
	// The one that came up has its page opened, and the one that is still
	// coming up has nothing opened for it.
	if got, want := r.opened(), []string{"http://127.0.0.1:6379"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := r.Messages["cache"].Text; got != "cache is up" {
		t.Errorf("got %q, want cache reported as up", got)
	}
	// And the reports of the two are kept apart.
	if got := r.Messages["dev"].Text; !strings.Contains(got, "no route to host") {
		t.Errorf("got %q, want dev reported as still waiting, with the reason", got)
	}
}

// TestPageScriptMemberOfStartingGroup covers the one restriction: a connection
// that a group is being started with cannot be started or stopped on its own
// meanwhile, and nor can a group while one of its connections is.
func TestPageScriptMemberOfStartingGroup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		env    string
		clicks []pageClick
		target string
		want   string
	}{
		{
			name:   "start a member of a group that is starting",
			env:    "PCONN_PENDING=dev",
			clicks: []pageClick{startClick("dev", "", "dev-web", "db"), startClick("db", "", "db")},
			target: "db",
			want:   "cannot start db while dev is being started",
		},
		{
			name:   "stop a member of a group that is starting",
			env:    "PCONN_PENDING=dev",
			clicks: []pageClick{startClick("dev", "", "dev-web", "db"), stopClick("db", "db")},
			target: "db",
			want:   "cannot stop db while dev is being started",
		},
		{
			name:   "start a group one of whose members is starting",
			env:    "PCONN_PENDING=db",
			clicks: []pageClick{startClick("db", "", "db"), startClick("dev", "", "dev-web", "db")},
			target: "dev",
			want:   "cannot start dev while db is being started",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runPageScript(t, []string{tc.env}, tc.clicks...)
			msg := r.Messages[tc.target]
			if !msg.Bad || msg.Text != tc.want {
				t.Errorf("got %+v, want %q", msg, tc.want)
			}
			second := tc.clicks[1]
			if r.fetched("POST", "/api/"+second.Kind+"?target="+second.Target) {
				t.Errorf("the refused %v of %v was sent anyway", second.Kind, second.Target)
			}
		})
	}
}

// TestPageScriptStopCancelsStart covers stopping a group that is being started
// and failing to come up, which is how one is given up on: the start is
// cancelled, the stop is sent after the start rather than overtaken by it,
// and nothing is opened for the group.
func TestPageScriptStopCancelsStart(t *testing.T) {
	r := runPageScript(t, []string{"PCONN_PENDING=dev"},
		startClick("dev", "http://127.0.0.1:8080", "dev-web", "db"),
		stopClick("dev", "dev-web", "db"),
	)
	start, stop := r.fetchIndex("POST", "/api/start?target=dev"), r.fetchIndex("POST", "/api/stop?target=dev")
	if start < 0 || stop < 0 {
		t.Fatalf("want both the start and the stop sent: %+v", r.Fetches)
	}
	if stop < start {
		t.Errorf("the stop was sent before the start it cancels: %+v", r.Fetches)
	}
	if len(r.Opened) != 0 {
		t.Errorf("pages were opened for a start that was cancelled: %v", r.opened())
	}
	if got := r.Messages["dev"].Text; got != "dev is stopped" {
		t.Errorf("got %q, want dev reported as stopped", got)
	}
}
