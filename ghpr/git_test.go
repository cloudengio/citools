// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"slices"
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
	if got, want := must(NewGit(run).Diff(t.Context(), "", "base", "head", excludedPaths)), "the diff\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Lock files and generated code say nothing about the intent of a change
	// and crowd out what does.
	for _, want := range []string{":!*go.sum", ":!*.pb.go", ":!*package-lock.json", ":!*.min.js"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the diff command does not exclude %v: %v", want, cmd)
		}
	}
	// Every exclusion has to apply at any depth, since the diff is taken from
	// the top of the repository and a bare name would only match there: a
	// module in a subdirectory has a go.sum of its own.
	for _, exclude := range excludedPaths {
		if !strings.HasPrefix(exclude, ":!*") {
			t.Errorf("%v only excludes that path in the directory git runs in", exclude)
		}
	}
	run.assertNoUnexpected()
}

// TestGitDiffInDir covers the directory the diff is taken from, which is what
// decides how much of the repository it covers: the diff is limited to the "."
// pathspec, so git has to run at the top level for it to cover all of it.
func TestGitDiffInDir(t *testing.T) {
	run := newFakeRunner(t).
		expect(diffCmdIn("/repo", "base", "head"), "the diff\n", nil).
		expect(diffStatCmdIn("/repo", "base", "head"), " x | 2 +-\n", nil)
	g := NewGit(run)

	if got, want := must(g.Diff(t.Context(), "/repo", "base", "head", excludedPaths)), "the diff\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := must(g.DiffStat(t.Context(), "/repo", "base", "head")), " x | 2 +-\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// "-C <dir>" is a global git option, so it has to precede the subcommand
	// rather than follow it.
	for _, cmd := range run.ranCommands() {
		if !strings.HasPrefix(cmd, "git -C /repo diff") {
			t.Errorf("%v does not run git in /repo", cmd)
		}
	}
	run.assertNoUnexpected()
}

func TestGitToplevel(t *testing.T) {
	run := newFakeRunner(t).
		expect("git rev-parse --show-toplevel", "/repo/top\n", nil)
	if got, want := must(NewGit(run).Toplevel(t.Context())), "/repo/top"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Outside a repository there is no top level, and the error says so
	// rather than reporting an empty path as one.
	run = newFakeRunner(t).
		expect("git rev-parse --show-toplevel", "", errors.New("not a git repository"))
	_, err := NewGit(run).Toplevel(t.Context())
	if err == nil || !strings.Contains(err.Error(), "cannot find the top level of the repository") {
		t.Errorf("got %v, want an error about the top level", err)
	}
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

func TestGitMergeFFOnly(t *testing.T) {
	run := newFakeRunner(t).expect("git merge --ff-only origin/main", "", nil)
	g := NewGit(run)
	if err := g.MergeFFOnly(t.Context(), "origin/main"); err != nil {
		t.Fatalf("MergeFFOnly: %v", err)
	}
	run.assertNoUnexpected()

	var announced []string
	dryRun := newFakeRunner(t)
	dg := NewGit(dryRun).DryRun(func(cmd string) { announced = append(announced, cmd) })
	if err := dg.MergeFFOnly(t.Context(), "origin/main"); err != nil {
		t.Fatalf("dry MergeFFOnly: %v", err)
	}
	if len(announced) != 1 || announced[0] != "git merge --ff-only origin/main" {
		t.Errorf("got %v, want dry-run announcement", announced)
	}
	dryRun.assertNoUnexpected()
}

func TestGitFetchUpdateBranch(t *testing.T) {
	run := newFakeRunner(t).expect("git fetch origin main:main", "", nil)
	g := NewGit(run)
	if err := g.FetchUpdateBranch(t.Context(), "main"); err != nil {
		t.Fatalf("FetchUpdateBranch: %v", err)
	}
	run.assertNoUnexpected()

	var announced []string
	dryRun := newFakeRunner(t)
	dg := NewGit(dryRun).DryRun(func(cmd string) { announced = append(announced, cmd) })
	if err := dg.FetchUpdateBranch(t.Context(), "main"); err != nil {
		t.Fatalf("dry FetchUpdateBranch: %v", err)
	}
	if len(announced) != 1 || announced[0] != "git fetch origin main:main" {
		t.Errorf("got %v, want dry-run announcement", announced)
	}
	dryRun.assertNoUnexpected()
}

func TestParseCommit(t *testing.T) {
	raw := "tree tree111\n" +
		"parent par111\n" +
		"parent par222\n" +
		"author Alice Example <alice@example.com> 1700000000 -0800\n" +
		"committer Bob Bot <noreply@example.com> 1700000100 +0000\n" +
		"gpgsig -----BEGIN PGP SIGNATURE-----\n" +
		" \n" +
		" abc123\n" +
		" -----END PGP SIGNATURE-----\n" +
		"\n" +
		"feat: subject (#42)\n\nold body\n"

	c := must(parseCommit(raw))
	if c.Tree != "tree111" {
		t.Errorf("tree = %q, want tree111", c.Tree)
	}
	if !slices.Equal(c.Parents, []string{"par111", "par222"}) {
		t.Errorf("parents = %v, want [par111 par222]", c.Parents)
	}
	if c.Author != (CommitIdentity{Name: "Alice Example", Email: "alice@example.com", Date: "1700000000 -0800"}) {
		t.Errorf("author = %+v", c.Author)
	}
	if c.Committer != (CommitIdentity{Name: "Bob Bot", Email: "noreply@example.com", Date: "1700000100 +0000"}) {
		t.Errorf("committer = %+v", c.Committer)
	}
	if c.Subject() != "feat: subject (#42)" {
		t.Errorf("subject = %q", c.Subject())
	}
	if c.Message != "feat: subject (#42)\n\nold body\n" {
		t.Errorf("message = %q", c.Message)
	}
}

func TestGitRewriteHelpers(t *testing.T) {
	raw := "tree tree111\nparent par111\nauthor A <a@b> 100 +0000\ncommitter C <c@d> 200 +0000\n\nsubject\n"
	commitCmd := "git commit-tree tree111 -p newpar111 -F -"
	run := newFakeRunner(t).
		expect("git status --porcelain", "", nil).
		expect("git merge-base sha1 sha2", "sha1\n", nil).
		expect("git merge-base sha3 sha2", "sha0\n", nil).
		expect("git rev-list --ancestry-path --reverse --topo-order sha1..HEAD", "c1\nc2\n", nil).
		expect("git cat-file commit sha1", raw, nil).
		expect(commitCmd, "newsha1\n", nil).
		expect("git reset --keep newsha1", "", nil).
		expect("git push --force-with-lease origin main", "", nil)

	g := NewGit(run)
	if dirty := must(g.WorkingTreeDirty(t.Context())); dirty {
		t.Error("expected clean working tree")
	}
	if !must(g.IsAncestor(t.Context(), "sha1", "sha2")) {
		t.Error("expected sha1 to be ancestor of sha2")
	}
	if must(g.IsAncestor(t.Context(), "sha3", "sha2")) {
		t.Error("expected sha3 not to be ancestor of sha2")
	}
	if got := must(g.RevList(t.Context(), "sha1..HEAD")); !slices.Equal(got, []string{"c1", "c2"}) {
		t.Errorf("RevList = %v", got)
	}
	c := must(g.ReadCommit(t.Context(), "sha1"))
	newSHA := must(g.CommitTree(t.Context(), c, []string{"newpar111"}, "subject\n\nnote\n"))
	if newSHA != "newsha1" {
		t.Errorf("CommitTree = %q, want newsha1", newSHA)
	}
	if got := run.stdinFor("git commit-tree"); got != "subject\n\nnote\n" {
		t.Errorf("commit-tree stdin = %q", got)
	}
	wantEnv := []string{
		"GIT_AUTHOR_NAME=A",
		"GIT_AUTHOR_EMAIL=a@b",
		"GIT_AUTHOR_DATE=100 +0000",
		"GIT_COMMITTER_NAME=C",
		"GIT_COMMITTER_EMAIL=c@d",
		"GIT_COMMITTER_DATE=200 +0000",
	}
	if got := run.envFor("git commit-tree"); !slices.Equal(got, wantEnv) {
		t.Errorf("commit-tree env = %v, want %v", got, wantEnv)
	}
	if err := g.ResetKeep(t.Context(), newSHA); err != nil {
		t.Fatalf("ResetKeep: %v", err)
	}
	if err := g.PushForceWithLease(t.Context(), "main"); err != nil {
		t.Fatalf("PushForceWithLease: %v", err)
	}
	run.assertNoUnexpected()
}

func TestGitNotesListAndLogSubjects(t *testing.T) {
	run := newFakeRunner(t).
		expect("git notes list", "note1 sha2\nnote2 sha1\n", nil).
		expect("git log --format=%H%x09%s HEAD", "sha3\tthird\nsha2\tsecond\nsha1\tfirst\n", nil)

	g := NewGit(run)
	noted := must(g.NotesList(t.Context()))
	if len(noted) != 2 || !noted["sha1"] || !noted["sha2"] {
		t.Errorf("NotesList = %v", noted)
	}
	commits := must(g.LogSubjects(t.Context(), "HEAD"))
	want := []CommitSummary{
		{SHA: "sha3", Subject: "third"},
		{SHA: "sha2", Subject: "second"},
		{SHA: "sha1", Subject: "first"},
	}
	if !slices.Equal(commits, want) {
		t.Errorf("LogSubjects = %v, want %v", commits, want)
	}
	run.assertNoUnexpected()
}

func TestGitStageAndCommit(t *testing.T) {
	run := newFakeRunner(t).
		expect("git status --short --untracked-files=no", " M a.go\n", nil).
		expect("git add -u", "", nil).
		expect("git commit -F -", "", nil)

	g := NewGit(run)
	status := must(g.StatusShort(t.Context()))
	if status != " M a.go" {
		t.Errorf("StatusShort = %q", status)
	}
	if err := g.StageTracked(t.Context()); err != nil {
		t.Fatalf("StageTracked: %v", err)
	}
	if err := g.Commit(t.Context(), "feat: new changes"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := run.stdinFor("git commit -F -"); got != "feat: new changes" {
		t.Errorf("commit stdin = %q", got)
	}
	run.assertNoUnexpected()

	var announced []string
	dry := newFakeRunner(t)
	dg := NewGit(dry).DryRun(func(cmd string) { announced = append(announced, cmd) })
	if err := dg.StageTracked(t.Context()); err != nil {
		t.Fatalf("dry StageTracked: %v", err)
	}
	if err := dg.Commit(t.Context(), "feat: dry"); err != nil {
		t.Fatalf("dry Commit: %v", err)
	}
	if !slices.Equal(announced, []string{"git add -u", "git commit -F -"}) {
		t.Errorf("dry announced = %v", announced)
	}
	dry.assertNoUnexpected()
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
