// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestGitReads(t *testing.T) {
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "my-feature\n", nil).
		expect("git rev-parse my-feature", "abcdef1234567890\n", nil).
		expect("git rev-parse my-feature^{tree}", "treehash12345678\n", nil).
		expect("git log -1 --pretty=%s my-feature", "chore: update deps\n", nil).
		expect("git ls-remote --heads origin my-feature", "abc\trefs/heads/my-feature\n", nil).
		expect("git ls-remote --heads origin gone", "", nil)
	g := NewGit(run)

	// Output is trimmed: git's trailing newline is not part of the value.
	if got, want := must(g.CurrentBranch(t.Context())), "my-feature"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := must(g.RevParse(t.Context(), "my-feature")), "abcdef1234567890"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := must(g.TreeHash(t.Context(), "my-feature")), "treehash12345678"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := must(g.Subject(t.Context(), "my-feature")), "chore: update deps"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// A branch is on origin when ls-remote names it, and not when it says
	// nothing, which is not an error.
	if got := must(g.RemoteBranchExists(t.Context(), "my-feature")); !got {
		t.Error("my-feature should be on origin")
	}
	if got := must(g.RemoteBranchExists(t.Context(), "gone")); got {
		t.Error("gone should not be on origin")
	}
	run.assertNoUnexpected()
}

func TestGitMergeBase(t *testing.T) {
	// The remote-tracking branch is what the pull request will be merged
	// into, so it is preferred.
	run := newFakeRunner(t).
		expect("git merge-base my-feature origin/main", "fromorigin\n", nil)
	if got, want := must(NewGit(run).MergeBase(t.Context(), "my-feature", "main")), "fromorigin"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Falling back to the local branch when there is no remote-tracking one.
	run = newFakeRunner(t).
		expect("git merge-base my-feature origin/main", "", errors.New("unknown revision")).
		expect("git merge-base my-feature main", "fromlocal\n", nil)
	if got, want := must(NewGit(run).MergeBase(t.Context(), "my-feature", "main")), "fromlocal"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Neither: the error says what was being looked for.
	run = newFakeRunner(t).
		expect("git merge-base my-feature origin/main", "", errors.New("no")).
		expect("git merge-base my-feature main", "", errors.New("no"))
	_, err := NewGit(run).MergeBase(t.Context(), "my-feature", "main")
	if err == nil || !strings.Contains(err.Error(), "no merge base for my-feature and main") {
		t.Errorf("got %v, want an error naming both refs", err)
	}
}

func TestGitDiffExcludesGeneratedFiles(t *testing.T) {
	cmd := diffCmd("base", "head")
	run := newFakeRunner(t).expect(cmd, "the diff\n", nil)
	if got, want := must(NewGit(run).Diff(t.Context(), "base", "head", excludedPaths)), "the diff\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Lock files and generated code say nothing about the intent of a change
	// and crowd out what does.
	for _, want := range []string{":!go.sum", ":!*.pb.go", ":!package-lock.json", ":!*.min.js"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the diff command does not exclude %v: %v", want, cmd)
		}
	}
	run.assertNoUnexpected()
}

func TestGitNotes(t *testing.T) {
	run := newFakeRunner(t).
		expect("git notes add --force --file - deadbeef", "", nil).
		expect("git notes show deadbeef", "an existing note\n", nil).
		expect("git notes show nonote", "", errors.New("no note found")).
		expect("git push origin refs/notes/*", "", nil)
	g := NewGit(run)

	// The note is given to git on its standard input, since it is multi-line
	// text and may be long.
	if err := g.AddNote(t.Context(), "deadbeef", "a\nnote\n"); err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	if got, want := run.stdinFor("git notes add"), "a\nnote\n"; got != want {
		t.Errorf("got %q on stdin, want %q", got, want)
	}

	// A commit with no note reads as no note rather than an error: it is
	// only used to say whether one is being replaced.
	if got, want := g.Note(t.Context(), "deadbeef"), "an existing note"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := g.Note(t.Context(), "nonote"); got != "" {
		t.Errorf("got %q, want an empty string", got)
	}
	if err := g.PushNotes(t.Context()); err != nil {
		t.Errorf("PushNotes: %v", err)
	}
	run.assertNoUnexpected()
}

func TestGitWrites(t *testing.T) {
	run := newFakeRunner(t).
		expect("git push --set-upstream origin my-feature", "", nil).
		expect("git branch --delete my-feature", "", nil).
		expect("git push origin --delete my-feature", "", nil).
		expect("git fetch origin main", "", nil)
	g := NewGit(run)
	for _, op := range []struct {
		name string
		err  error
	}{
		{"PushBranch", g.PushBranch(t.Context(), "my-feature")},
		{"DeleteBranch", g.DeleteBranch(t.Context(), "my-feature")},
		{"DeleteRemoteBranch", g.DeleteRemoteBranch(t.Context(), "my-feature")},
		{"Fetch", g.Fetch(t.Context(), "main")},
	} {
		if op.err != nil {
			t.Errorf("%v: %v", op.name, op.err)
		}
	}
	// Deleting a branch is not forced: git's own check that it was merged is
	// worth keeping, since this tool's idea of merged is a comparison of
	// content rather than of history.
	for _, cmd := range run.ranCommands() {
		if strings.Contains(cmd, "branch --delete --force") || strings.Contains(cmd, "branch -D") {
			t.Errorf("%v forces the delete", cmd)
		}
	}
	run.assertNoUnexpected()
}

func TestGitDryRun(t *testing.T) {
	var announced []string
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "my-feature\n", nil)
	g := NewGit(run).DryRun(func(cmd string) { announced = append(announced, cmd) })

	// A dry run still reads: it has to, to say what it would do.
	if got, want := must(g.CurrentBranch(t.Context())), "my-feature"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// It changes nothing, and says what it would have changed.
	if err := g.AddNote(t.Context(), "deadbeef", "a note"); err != nil {
		t.Errorf("AddNote: %v", err)
	}
	if err := g.PushNotes(t.Context()); err != nil {
		t.Errorf("PushNotes: %v", err)
	}
	if run.ranPrefix("git notes") || run.ranPrefix("git push") {
		t.Errorf("a dry run ran a command that changes something: %v", run.ranCommands())
	}
	want := []string{"git notes add --force --file - deadbeef", "git push origin refs/notes/*"}
	if len(announced) != len(want) {
		t.Fatalf("got %v, want %v", announced, want)
	}
	for i := range want {
		if announced[i] != want[i] {
			t.Errorf("got %q, want %q", announced[i], want[i])
		}
	}
	run.assertNoUnexpected()
}

// must returns the value of a call that this test requires to succeed. It
// takes exactly the two results of such a call, so that it can be written as
// must(g.CurrentBranch(ctx)); a panic in a test is reported as a failure, and
// these are calls whose failure means the test itself is wrong.
func must[T any](v T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("unexpected error: %v", err))
	}
	return v
}
