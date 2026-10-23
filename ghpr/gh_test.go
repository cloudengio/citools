// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// prJSON is what gh pr view --json returns, with the fields ghpr asks for.
const prJSON = `{
  "number": 42,
  "state": "MERGED",
  "body": "## Overview\nit does a thing",
  "title": "chore: update deps",
  "url": "https://github.com/o/r/pull/42",
  "headRefName": "my-feature",
  "headRefOid": "feature1234567890",
  "mergeCommit": {"oid": "squash1234567890"}
}`

func viewCmd(target string) string {
	return "gh pr view " + target + " --json " + prFields
}

func TestGHView(t *testing.T) {
	run := newFakeRunner(t).expect(viewCmd("my-feature"), prJSON, nil)
	pr := must(NewGH(run).View(t.Context(), "my-feature"))

	if got, want := pr.Number, 42; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if got, want := pr.Title, "chore: update deps"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := pr.Body, "## Overview\nit does a thing"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := pr.URL, "https://github.com/o/r/pull/42"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := pr.HeadRefOid, "feature1234567890"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if !pr.Merged() {
		t.Error("the pull request is merged")
	}
	// The merge commit is the one holding the change on the base branch,
	// which is what a note is attached to.
	if got, want := pr.MergeSHA(), "squash1234567890"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	run.assertNoUnexpected()
}

func TestGHViewOpen(t *testing.T) {
	// An open pull request has no merge commit, which must read as none
	// rather than panicking on the absent field.
	open := `{"number": 7, "state": "OPEN", "headRefName": "my-feature", "mergeCommit": null}`
	run := newFakeRunner(t).expect(viewCmd("my-feature"), open, nil)
	pr := must(NewGH(run).View(t.Context(), "my-feature"))
	if pr.Merged() {
		t.Error("an open pull request is not merged")
	}
	if got := pr.MergeSHA(); got != "" {
		t.Errorf("got %q, want an empty string", got)
	}
}

func TestGHViewNoPR(t *testing.T) {
	// gh reports a branch with no pull request the same way it reports a
	// failure, so the message is what distinguishes them. create-pr relies
	// on telling them apart to know there is one to create.
	for _, msg := range []string{
		"gh pr view x: exit status 1: no pull requests found for branch \"x\"",
		"gh pr view x: exit status 1: GraphQL: Could not resolve to a PullRequest with the number of 9",
	} {
		run := newFakeRunner(t).expect(viewCmd("x"), "", errors.New(msg))
		_, err := NewGH(run).View(t.Context(), "x")
		if !errors.Is(err, ErrNoPR) {
			t.Errorf("%v: got %v, want ErrNoPR", msg, err)
		}
	}

	// Anything else is a real failure and must not be mistaken for a missing
	// pull request, or create-pr would go on to create a second one.
	run := newFakeRunner(t).expect(viewCmd("x"), "", errors.New("gh: not authenticated"))
	_, err := NewGH(run).View(t.Context(), "x")
	if err == nil || errors.Is(err, ErrNoPR) {
		t.Errorf("got %v, want a real error", err)
	}
}

func TestGHViewBadJSON(t *testing.T) {
	run := newFakeRunner(t).expect(viewCmd("x"), "not json", nil)
	_, err := NewGH(run).View(t.Context(), "x")
	if err == nil || !strings.Contains(err.Error(), "cannot parse the pull request") {
		t.Errorf("got %v, want a parse error", err)
	}
}

func TestGHCreate(t *testing.T) {
	cmd := "gh pr create --base main --head my-feature --title " +
		`"chore: update deps"` + " --body-file -"
	run := newFakeRunner(t).expect(cmd, "https://github.com/o/r/pull/43\n", nil)
	url := must(NewGH(run).Create(t.Context(), "main", "my-feature", "chore: update deps", "the body", false))

	if got, want := url, "https://github.com/o/r/pull/43"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// The body goes on standard input: a generated description can be longer
	// than a command line is allowed to be.
	if got, want := run.stdinFor("gh pr create"), "the body"; got != want {
		t.Errorf("got %q on stdin, want %q", got, want)
	}
	run.assertNoUnexpected()
}

func TestGHCreateDraft(t *testing.T) {
	run := newFakeRunner(t).expectPrefix("gh pr create", "url\n", nil)
	if _, err := NewGH(run).Create(t.Context(), "main", "b", "t", "body", true); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !run.ranPrefix("gh pr create") {
		t.Fatal("gh pr create was not run")
	}
	var cmd string
	for _, c := range run.ranCommands() {
		if strings.HasPrefix(c, "gh pr create") {
			cmd = c
		}
	}
	if !strings.Contains(cmd, "--draft") {
		t.Errorf("%v does not pass --draft", cmd)
	}
}

func TestGHUpdateBody(t *testing.T) {
	run := newFakeRunner(t).expect("gh pr edit 42 --body-file -", "", nil)
	if err := NewGH(run).UpdateBody(t.Context(), 42, "the new body"); err != nil {
		t.Fatalf("UpdateBody: %v", err)
	}
	if got, want := run.stdinFor("gh pr edit"), "the new body"; got != want {
		t.Errorf("got %q on stdin, want %q", got, want)
	}
	run.assertNoUnexpected()
}

func TestGHDryRun(t *testing.T) {
	var announced []string
	run := newFakeRunner(t).expect(viewCmd("my-feature"), prJSON, nil)
	gh := NewGH(run).DryRun(func(cmd string) { announced = append(announced, cmd) })

	// Reading is still done, so that a dry run can report what it found.
	if _, err := gh.View(t.Context(), "my-feature"); err != nil {
		t.Fatalf("View: %v", err)
	}
	if err := gh.UpdateBody(t.Context(), 42, "body"); err != nil {
		t.Fatalf("UpdateBody: %v", err)
	}
	if run.ranPrefix("gh pr edit") {
		t.Error("a dry run edited the pull request")
	}
	if len(announced) != 1 || announced[0] != "gh pr edit 42 --body-file -" {
		t.Errorf("got %v, want the edit to be announced", announced)
	}
	run.assertNoUnexpected()
}

func TestIsTransient(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		// What the api being unreachable looks like, including the failure
		// that prompted this.
		{"tls handshake", errors.New(`gh pr view x: exit status 1: Post "https://api.github.com/graphql": net/http: TLS handshake timeout`), true},
		{"io timeout", errors.New("exit status 1: dial tcp: i/o timeout"), true},
		{"reset", errors.New("exit status 1: read: connection reset by peer"), true},
		{"dns", errors.New("exit status 1: dial tcp: lookup api.github.com: no such host"), true},
		{"bad gateway", errors.New("exit status 1: HTTP 502 Bad Gateway"), true},
		{"too quickly", errors.New("exit status 1: was submitted too quickly"), true},
		// An answer from github, which repeating would not change.
		{"no pull request", fmt.Errorf("%w for x", ErrNoPR), false},
		{"unauthenticated", errors.New("exit status 4: gh: Not authenticated"), false},
		{"unresolvable", errors.New("exit status 1: Could not resolve to a PullRequest"), false},
		{"nil", nil, false},
	} {
		if got := isTransient(tc.err); got != tc.want {
			t.Errorf("%v: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestGHViewRetriesTransient(t *testing.T) {
	tlsTimeout := errors.New(`gh pr view my-feature: exit status 1: Post "https://api.github.com/graphql": net/http: TLS handshake timeout`)
	run := newFakeRunner(t).expectSeq(viewCmd("my-feature"),
		fakeResult{err: tlsTimeout},
		fakeResult{err: tlsTimeout},
		fakeResult{out: prJSON})

	var notices int
	gh := NewGH(run)
	gh.retryDelay = 0
	gh.notify = func(string, ...any) { notices++ }

	pr := must(gh.View(t.Context(), "my-feature"))
	if got, want := pr.Number, 42; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if got, want := len(run.ranCommands()), 3; got != want {
		t.Errorf("got %v attempts, want %v", got, want)
	}
	// A pause of seconds should say why rather than look like a hang.
	if got, want := notices, 2; got != want {
		t.Errorf("got %v notices, want %v", got, want)
	}
}

func TestGHViewGivesUpEventually(t *testing.T) {
	tlsTimeout := errors.New("exit status 1: net/http: TLS handshake timeout")
	run := newFakeRunner(t).expect(viewCmd("my-feature"), "", tlsTimeout)
	gh := NewGH(run)
	gh.retryDelay = 0

	_, err := gh.View(t.Context(), "my-feature")
	if err == nil {
		t.Fatal("expected an error")
	}
	// The error says it was retried, and still carries the cause.
	for _, want := range []string{"could not be reached in 3 attempts", "TLS handshake timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
	if got, want := len(run.ranCommands()), 3; got != want {
		t.Errorf("got %v attempts, want %v", got, want)
	}
}

func TestGHViewDoesNotRetryAnswers(t *testing.T) {
	// An answer from github is final: repeating it would only be slower, and
	// create-pr relies on a missing pull request arriving promptly.
	for _, tc := range []struct{ name, msg string }{
		{"no pull request", `exit status 1: no pull requests found for branch "my-feature"`},
		{"unauthenticated", "exit status 4: gh: Not authenticated"},
	} {
		run := newFakeRunner(t).expect(viewCmd("my-feature"), "", errors.New(tc.msg))
		gh := NewGH(run)
		gh.retryDelay = 0
		if _, err := gh.View(t.Context(), "my-feature"); err == nil {
			t.Errorf("%v: expected an error", tc.name)
		}
		if got, want := len(run.ranCommands()), 1; got != want {
			t.Errorf("%v: got %v attempts, want %v", tc.name, got, want)
		}
	}
}

func TestGHViewRetryRespectsContext(t *testing.T) {
	run := newFakeRunner(t).expect(viewCmd("my-feature"), "",
		errors.New("exit status 1: net/http: TLS handshake timeout"))
	gh := NewGH(run)
	gh.retryDelay = time.Hour // Only the context can end this.

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := gh.View(ctx, "my-feature")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}
