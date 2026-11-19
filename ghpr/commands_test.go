// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const noPRErr = "exit status 1: no pull requests found for branch \"my-feature\""

// onFeatureBranch scripts the state every command starts by establishing.
func onFeatureBranch(run *fakeRunner) *fakeRunner {
	return run.expect("git rev-parse --abbrev-ref HEAD", "my-feature\n", nil)
}

// readyToSummarize scripts a branch that is on origin with a diff to describe
// and an agent that describes it.
func readyToSummarize(run *fakeRunner, summary string) *fakeRunner {
	return run.
		expect("git ls-remote --heads origin my-feature", "sha\trefs/heads/my-feature\n", nil).
		expect("git merge-base my-feature origin/main", "mergebase\n", nil).
		expect(diffCmd("mergebase", "my-feature"), "a diff\n", nil).
		expect(`claude -p --allowedTools ""`, summary, nil)
}

func TestCreatePR(t *testing.T) {
	run := newFakeRunner(t)
	onFeatureBranch(run).
		expect(viewCmd("my-feature"), "", errors.New(noPRErr)).
		expect("git status --short --untracked-files=all", "", nil)
	readyToSummarize(run, "## Overview\nit does a thing\n").
		expect(`gh pr create --base main --head my-feature --title "chore: update deps" --body-file -`,
			"https://github.com/o/r/pull/7\n", nil)

	app, out := testApp(t, run, "y\n")
	if err := app.CreatePR(t.Context(), "chore: update deps", false, true); err != nil {
		t.Fatalf("CreatePR: %v", err)
	}

	// The generated description is shown before anything is created, and is
	// what is sent as the body.
	for _, want := range []string{"it does a thing", "https://github.com/o/r/pull/7"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output does not contain %q:\n%v", want, out)
		}
	}
	if got, want := run.stdinFor("gh pr create"), "## Overview\nit does a thing"; got != want {
		t.Errorf("got %q as the body, want %q", got, want)
	}
	run.assertNoUnexpected()
}

func TestCreatePRDraft(t *testing.T) {
	run := newFakeRunner(t)
	onFeatureBranch(run).
		expect(viewCmd("my-feature"), "", errors.New(noPRErr)).
		expect("git status --short --untracked-files=all", "", nil)
	readyToSummarize(run, "a summary").
		expectPrefix("gh pr create", "url\n", nil)

	app, out := testApp(t, run, "y\n")
	if err := app.CreatePR(t.Context(), "t", true, true); err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	// What is about to happen is described accurately, since that is what is
	// being agreed to.
	if !strings.Contains(out.String(), "draft pull request") {
		t.Errorf("the confirmation does not say it is a draft:\n%v", out)
	}
}

func TestCreatePRShowCommand(t *testing.T) {
	run := newFakeRunner(t)
	onFeatureBranch(run).
		expect(viewCmd("my-feature"), "", errors.New(noPRErr)).
		expect("git merge-base my-feature origin/main", "mergebase\n", nil).
		expect(diffCmd("mergebase", "my-feature"), "a diff\n", nil)

	app, out := testApp(t, run, "")
	app.ShowSummaryCommand = true
	if err := app.CreatePR(t.Context(), "t", false, true); err != nil {
		t.Fatalf("CreatePR: %v", err)
	}

	if !strings.Contains(out.String(), `claude -p --allowedTools "" << 'EOF'`) {
		t.Errorf("expected command to be output, got:\n%v", out)
	}
	if !strings.Contains(out.String(), "a diff") {
		t.Errorf("expected output to contain diff, got:\n%v", out)
	}
	// The agent must NOT be invoked, nor anything created or pushed.
	if run.ranPrefix("claude") || run.ranPrefix("gh pr create") || run.ranPrefix("git push") {
		t.Errorf("unexpected commands were run: %v", run.ranCommands())
	}
	run.assertNoUnexpected()
}

func TestCreatePROnBaseBranch(t *testing.T) {
	// Nothing to describe: the base branch is not a change.
	run := newFakeRunner(t).expect("git rev-parse --abbrev-ref HEAD", "main\n", nil)
	app, _ := testApp(t, run, "")
	err := app.CreatePR(t.Context(), "t", false, true)
	if err == nil || !strings.Contains(err.Error(), "main is the base branch") {
		t.Errorf("got %v, want a refusal to run on the base branch", err)
	}
	if run.ranPrefix("gh") {
		t.Error("github was consulted before the branch was checked")
	}
}

func TestCreatePRAlreadyExists(t *testing.T) {
	// A second pull request for the same branch is not what was wanted, so
	// the error names the one that exists and what to do instead.
	run := newFakeRunner(t)
	onFeatureBranch(run).expect(viewCmd("my-feature"), prJSON, nil)
	app, _ := testApp(t, run, "")
	err := app.CreatePR(t.Context(), "t", false, true)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"#42", "already exists", "update-summary"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
	if run.ranPrefix("claude") {
		t.Error("the agent was run for a pull request that already exists")
	}
}

func TestCreatePRViewFails(t *testing.T) {
	// A failure to ask github must not be read as there being no pull
	// request, or a second one would be created.
	run := newFakeRunner(t)
	onFeatureBranch(run).expect(viewCmd("my-feature"), "", errors.New("gh: not authenticated"))
	app, _ := testApp(t, run, "")
	err := app.CreatePR(t.Context(), "t", false, true)
	if err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("got %v, want the error from gh", err)
	}
}

func TestCreatePRPushesBranch(t *testing.T) {
	run := newFakeRunner(t)
	onFeatureBranch(run).
		expect(viewCmd("my-feature"), "", errors.New(noPRErr)).
		expect("git status --short --untracked-files=all", "", nil).
		expect("git ls-remote --heads origin my-feature", "", nil).
		expect("git push --set-upstream origin my-feature", "", nil).
		expect("git merge-base my-feature origin/main", "mergebase\n", nil).
		expect(diffCmd("mergebase", "my-feature"), "a diff\n", nil).
		expect(`claude -p --allowedTools ""`, "a summary", nil).
		expectPrefix("gh pr create", "url\n", nil)

	app, out := testApp(t, run, "y\ny\n")
	if err := app.CreatePR(t.Context(), "t", false, true); err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if !run.ran("git push --set-upstream origin my-feature") {
		t.Errorf("the branch was not pushed: %v", run.ranCommands())
	}
	if !strings.Contains(out.String(), "not on origin yet") {
		t.Errorf("the push was not asked about:\n%v", out)
	}
	run.assertNoUnexpected()
}

func TestCreatePRWithoutPush(t *testing.T) {
	// --push=false says not to, so the error says what to do rather than
	// doing it anyway.
	run := newFakeRunner(t)
	onFeatureBranch(run).
		expect(viewCmd("my-feature"), "", errors.New(noPRErr)).
		expect("git status --short --untracked-files=all", "", nil).
		expect("git ls-remote --heads origin my-feature", "", nil)
	app, _ := testApp(t, run, "")
	err := app.CreatePR(t.Context(), "t", false, false)
	if err == nil || !strings.Contains(err.Error(), "is not on origin") {
		t.Errorf("got %v, want a refusal to create a pull request for an unpushed branch", err)
	}
	if run.ranPrefix("git push") {
		t.Error("the branch was pushed despite --push=false")
	}
}

func TestCreatePRDeclined(t *testing.T) {
	run := newFakeRunner(t)
	onFeatureBranch(run).
		expect(viewCmd("my-feature"), "", errors.New(noPRErr)).
		expect("git status --short --untracked-files=all", "", nil)
	readyToSummarize(run, "a summary")

	// Answering no stops before anything is created, and is not an error.
	app, out := testApp(t, run, "n\n")
	if err := app.CreatePR(t.Context(), "t", false, true); err != nil {
		t.Fatalf("got %v, want no error for a declined action", err)
	}
	if run.ranPrefix("gh pr create") {
		t.Error("a pull request was created after being declined")
	}
	if !strings.Contains(out.String(), "aborted") {
		t.Errorf("the output does not say it was aborted:\n%v", out)
	}
}

func TestCreatePRCommitsUncommittedChanges(t *testing.T) {
	t.Run("branch already on origin pushes new commit", func(t *testing.T) {
		run := newFakeRunner(t)
		onFeatureBranch(run).
			expect(viewCmd("my-feature"), "", errors.New(noPRErr)).
			expect("git status --short --untracked-files=all", " M a.go\n?? b.go\n", nil).
			expect("git add -A", "", nil).
			expect("git commit -F -", "", nil).
			expect("git push --set-upstream origin my-feature", "", nil)
		readyToSummarize(run, "## Overview\nincludes uncommitted work").
			expect(`gh pr create --base main --head my-feature --title "feat: uncommitted" --body-file -`,
				"https://github.com/o/r/pull/9\n", nil)

		app, out := testApp(t, run, "y\ny\ny\n")
		if err := app.CreatePR(t.Context(), "feat: uncommitted", false, true); err != nil {
			t.Fatalf("CreatePR: %v", err)
		}
		if got := run.stdinFor("git commit -F -"); got != "feat: uncommitted" {
			t.Errorf("commit message = %q, want %q", got, "feat: uncommitted")
		}
		for _, want := range []string{"uncommitted changes on my-feature", "committed changes on my-feature", "push the newly committed changes", "https://github.com/o/r/pull/9"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output missing %q:\n%v", want, out)
			}
		}
		run.assertNoUnexpected()
	})

	t.Run("branch not on origin yet commits and pushes once", func(t *testing.T) {
		run := newFakeRunner(t)
		onFeatureBranch(run).
			expect(viewCmd("my-feature"), "", errors.New(noPRErr)).
			expect("git status --short --untracked-files=all", " M a.go\n", nil).
			expect("git add -A", "", nil).
			expect("git commit -F -", "", nil).
			expect("git ls-remote --heads origin my-feature", "", nil).
			expect("git push --set-upstream origin my-feature", "", nil).
			expect("git merge-base my-feature origin/main", "mergebase\n", nil).
			expect(diffCmd("mergebase", "my-feature"), "a diff\n", nil).
			expect(`claude -p --allowedTools ""`, "summary", nil).
			expectPrefix("gh pr create", "url\n", nil)

		app, _ := testApp(t, run, "y\ny\ny\n")
		if err := app.CreatePR(t.Context(), "feat: first push", false, true); err != nil {
			t.Fatalf("CreatePR: %v", err)
		}
		if got := run.stdinFor("git commit -F -"); got != "feat: first push" {
			t.Errorf("commit message = %q", got)
		}
		run.assertNoUnexpected()
	})

	t.Run("without push fails after committing locally", func(t *testing.T) {
		run := newFakeRunner(t)
		onFeatureBranch(run).
			expect(viewCmd("my-feature"), "", errors.New(noPRErr)).
			expect("git status --short --untracked-files=all", " M a.go\n", nil).
			expect("git add -A", "", nil).
			expect("git commit -F -", "", nil).
			expect("git ls-remote --heads origin my-feature", "sha\trefs/heads/my-feature\n", nil)

		app, _ := testApp(t, run, "y\n")
		err := app.CreatePR(t.Context(), "feat: local commit", false, false)
		if err == nil || !strings.Contains(err.Error(), "new commits that are not on origin") {
			t.Errorf("got %v, want new-commits push error", err)
		}
		if run.ranPrefix("git push") || run.ranPrefix("gh pr create") {
			t.Errorf("unexpected push/create ran: %v", run.ranCommands())
		}
	})

	t.Run("declined stops before staging or creating pr", func(t *testing.T) {
		run := newFakeRunner(t)
		onFeatureBranch(run).
			expect(viewCmd("my-feature"), "", errors.New(noPRErr)).
			expect("git status --short --untracked-files=all", " M a.go\n", nil)

		app, _ := testApp(t, run, "n\n")
		err := app.CreatePR(t.Context(), "feat: declined", false, true)
		if err == nil || !strings.Contains(err.Error(), "must be committed") {
			t.Errorf("got %v, want commit-required error", err)
		}
		if run.ranPrefix("git add") || run.ranPrefix("git commit") || run.ranPrefix("gh pr create") {
			t.Errorf("unexpected commands ran: %v", run.ranCommands())
		}
	})

	t.Run("empty title refused when dirty", func(t *testing.T) {
		run := newFakeRunner(t)
		onFeatureBranch(run).
			expect(viewCmd("my-feature"), "", errors.New(noPRErr)).
			expect("git status --short --untracked-files=all", " M a.go\n", nil)

		app, _ := testApp(t, run, "")
		err := app.CreatePR(t.Context(), "   ", false, true)
		if err == nil || !strings.Contains(err.Error(), "empty title") {
			t.Errorf("got %v, want empty-title error", err)
		}
		if run.ranPrefix("git add") || run.ranPrefix("git commit") {
			t.Errorf("staged/committed despite empty title: %v", run.ranCommands())
		}
	})
}

func TestCreatePRCommitUncommittedRealRepo(t *testing.T) {
	dir := t.TempDir()
	r := dirRunner{dir: dir}
	gitRun := func(args ...string) string {
		t.Helper()
		out, err := r.RunEnv(t.Context(), nil, nil, "git", args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}

	gitRun("init", "-b", "main")
	gitRun("config", "user.name", "Test User")
	gitRun("config", "user.email", "test@example.com")
	// Even with untracked display disabled in git config, untracked files must
	// still be detected, displayed, and committed.
	gitRun("config", "status.showUntrackedFiles", "no")

	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "tracked.txt")
	gitRun("commit", "-m", "initial")

	gitRun("switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("nested\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	app := &App{
		Git:    NewGit(r),
		GH:     NewGH(r),
		Base:   "main",
		Stdout: &out,
		Stdin:  strings.NewReader("y\ny\n"),
	}
	committed, err := app.commitUncommitted(t.Context(), "feature", "feat: commit working tree")
	if err != nil {
		t.Fatalf("commitUncommitted: %v", err)
	}
	if !committed {
		t.Fatal("expected committed == true")
	}
	if got := gitRun("log", "-1", "--format=%B"); got != "feat: commit working tree" {
		t.Errorf("commit message = %q", got)
	}
	committedFiles := gitRun("diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD")
	for _, want := range []string{"tracked.txt", "untracked.txt", "sub/nested.txt"} {
		if !strings.Contains(committedFiles, want) {
			t.Errorf("committed files missing %q:\n%s", want, committedFiles)
		}
		if !strings.Contains(out.String(), want) {
			t.Errorf("status preview missing %q:\n%s", want, out.String())
		}
	}
	if dirty := gitRun("status", "--porcelain", "--untracked-files=all"); dirty != "" {
		t.Errorf("working tree still dirty: %q", dirty)
	}

	// Untracked-only changes must also be detected and committed.
	if err := os.WriteFile(filepath.Join(dir, "only-untracked.txt"), []byte("alone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	committed, err = app.commitUncommitted(t.Context(), "feature", "feat: untracked only")
	if err != nil {
		t.Fatalf("commitUncommitted untracked-only: %v", err)
	}
	if !committed {
		t.Fatal("expected committed == true for untracked-only")
	}
	if got := gitRun("diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"); got != "only-untracked.txt" {
		t.Errorf("untracked-only committed files = %q, want only-untracked.txt", got)
	}
}

func TestUpdateSummary(t *testing.T) {
	openPR := `{"number":7,"state":"OPEN","body":"stale","url":"u","headRefName":"my-feature"}`
	run := newFakeRunner(t)
	onFeatureBranch(run).expect(viewCmd("my-feature"), openPR, nil)
	run.
		expect("git merge-base my-feature origin/main", "mergebase\n", nil).
		expect(diffCmd("mergebase", "my-feature"), "a diff\n", nil).
		expect(`claude -p --allowedTools ""`, "a fresh summary", nil).
		expect("gh pr edit 7 --body-file -", "", nil)

	app, out := testApp(t, run, "y\n")
	if err := app.UpdateSummary(t.Context(), ""); err != nil {
		t.Fatalf("UpdateSummary: %v", err)
	}
	if got, want := run.stdinFor("gh pr edit"), "a fresh summary"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if !strings.Contains(out.String(), "updated the description of #7") {
		t.Errorf("the output does not confirm the update:\n%v", out)
	}
	run.assertNoUnexpected()
}

func TestUpdateSummaryShowCommand(t *testing.T) {
	run := newFakeRunner(t)
	openPR := `{"number":7,"state":"OPEN","body":"old summary","url":"u","headRefName":"my-feature"}`
	onFeatureBranch(run).
		expect(viewCmd("my-feature"), openPR, nil).
		expect("git merge-base my-feature origin/main", "mergebase\n", nil).
		expect(diffCmd("mergebase", "my-feature"), "a diff\n", nil)

	app, out := testApp(t, run, "")
	app.ShowSummaryCommand = true
	if err := app.UpdateSummary(t.Context(), ""); err != nil {
		t.Fatalf("UpdateSummary: %v", err)
	}

	if !strings.Contains(out.String(), `claude -p --allowedTools "" << 'EOF'`) {
		t.Errorf("expected command to be output, got:\n%v", out)
	}
	if !strings.Contains(out.String(), "a diff") {
		t.Errorf("expected output to contain diff, got:\n%v", out)
	}
	// The agent must NOT be invoked, nor the PR updated.
	if run.ranPrefix("claude") || run.ranPrefix("gh pr edit") {
		t.Errorf("unexpected commands were run: %v", run.ranCommands())
	}
	run.assertNoUnexpected()
}

func TestUpdateSummaryUnchanged(t *testing.T) {
	// Regenerating the same description is not worth an api call, nor a
	// confirmation.
	openPR := `{"number":7,"state":"OPEN","body":"the same summary","url":"u","headRefName":"my-feature"}`
	run := newFakeRunner(t)
	onFeatureBranch(run).expect(viewCmd("my-feature"), openPR, nil)
	run.
		expect("git merge-base my-feature origin/main", "mergebase\n", nil).
		expect(diffCmd("mergebase", "my-feature"), "a diff\n", nil).
		expect(`claude -p --allowedTools ""`, "the same summary\n", nil)

	app, out := testApp(t, run, "")
	if err := app.UpdateSummary(t.Context(), ""); err != nil {
		t.Fatalf("UpdateSummary: %v", err)
	}
	if run.ranPrefix("gh pr edit") {
		t.Error("the description was replaced with itself")
	}
	if !strings.Contains(out.String(), "already this") {
		t.Errorf("the output does not say there was nothing to do:\n%v", out)
	}
	run.assertNoUnexpected()
}

func TestUpdateSummaryMerged(t *testing.T) {
	// A merged pull request has no branch left, so its head commit stands in
	// for one, and has to be fetched since the branch it was on is gone.
	run := newFakeRunner(t).
		expect(viewCmd("42"), prJSON, nil).
		expect("git fetch origin feature1234567890", "", nil).
		expect("git rev-parse feature1234567890", "feature1234567890\n", nil).
		expect("git merge-base feature1234567890 origin/main", "mergebase\n", nil).
		expect(diffCmd("mergebase", "feature1234567890"), "a diff\n", nil).
		expect(`claude -p --allowedTools ""`, "a summary of what was merged", nil).
		expect("gh pr edit 42 --body-file -", "", nil)

	app, _ := testApp(t, run, "y\n")
	if err := app.UpdateSummary(t.Context(), "42"); err != nil {
		t.Fatalf("UpdateSummary: %v", err)
	}
	if got, want := run.stdinFor("gh pr edit"), "a summary of what was merged"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	run.assertNoUnexpected()
}

func TestUpdateSummaryMergedHeadMissing(t *testing.T) {
	run := newFakeRunner(t).
		expect(viewCmd("42"), prJSON, nil).
		expect("git fetch origin feature1234567890", "", errors.New("not our ref")).
		expect("git rev-parse feature1234567890", "", errors.New("unknown revision"))
	app, _ := testApp(t, run, "")
	err := app.UpdateSummary(t.Context(), "42")
	if err == nil || !strings.Contains(err.Error(), "is not in this repository") {
		t.Errorf("got %v, want an error about the missing head commit", err)
	}
}

func TestUpdateSummaryOnBaseBranch(t *testing.T) {
	// On the base branch there is no branch to infer, so one has to be named.
	run := newFakeRunner(t).expect("git rev-parse --abbrev-ref HEAD", "main\n", nil)
	app, _ := testApp(t, run, "")
	err := app.UpdateSummary(t.Context(), "")
	if err == nil || !strings.Contains(err.Error(), "name the branch or the pull request") {
		t.Errorf("got %v, want a request for an argument", err)
	}
}

// mergedNoteState scripts post-note up to the point of attaching the note.
func mergedNoteState(run *fakeRunner, body string) *fakeRunner {
	pr := `{"number":42,"state":"MERGED","body":` + quoteJSON(body) +
		`,"url":"u","headRefName":"my-feature","headRefOid":"feature1","mergeCommit":{"oid":"squash1234567890"}}`
	return run.
		expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
		expect(viewCmd("my-feature"), pr, nil).
		expect("git fetch origin main", "", nil).
		expect("git rev-parse squash1234567890", "squash1234567890\n", nil).
		expect("git notes show squash1234567890", "", errors.New("no note")).
		expect("git notes add --force --file - squash1234567890", "", nil)
}

func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `"`, `\"`), "\n", `\n`) + `"`
}

func TestPostNote(t *testing.T) {
	run := newFakeRunner(t)
	mergedNoteState(run, "## Overview\n\nUses **claude** and `agy` \u2014 see [docs](d).").
		expect("git push origin refs/notes/*", "", nil)

	app, out := testApp(t, run, "y\n")
	if err := app.PostNote(t.Context(), "my-feature", true, false, false); err != nil {
		t.Fatalf("PostNote: %v", err)
	}

	// The note is the description as plain ascii: that is what reads well
	// from git log, and what the markdown would not.
	note := run.stdinFor("git notes add")
	want := "--- Overview\n\nUses claude and agy -- see docs (d)."
	if note != want {
		t.Errorf("got note %q, want %q", note, want)
	}
	if !isASCII(note) {
		t.Errorf("the note is not ascii: %q", note)
	}
	for _, msg := range []string{"attached the description of #42", "pushed refs/notes/*"} {
		if !strings.Contains(out.String(), msg) {
			t.Errorf("the output does not contain %q:\n%v", msg, out)
		}
	}
	run.assertNoUnexpected()
}

func TestPostNoteWithoutPushing(t *testing.T) {
	run := newFakeRunner(t)
	mergedNoteState(run, "a summary")
	app, out := testApp(t, run, "y\n")
	if err := app.PostNote(t.Context(), "my-feature", false, false, false); err != nil {
		t.Fatalf("PostNote: %v", err)
	}
	if run.ranPrefix("git push") {
		t.Error("notes were pushed despite --push-notes=false")
	}
	if strings.Contains(out.String(), "push refs/notes") {
		t.Errorf("the confirmation offered to push:\n%v", out)
	}
	run.assertNoUnexpected()
}

func TestPostNoteReplacesExisting(t *testing.T) {
	// Replacing a note is worth saying before it happens, since the one
	// being replaced is not recoverable.
	run := newFakeRunner(t)
	mergedNoteState(run, "a summary").
		expect("git notes show squash1234567890", "an older note", nil)
	app, out := testApp(t, run, "y\n")
	if err := app.PostNote(t.Context(), "my-feature", false, false, false); err != nil {
		t.Fatalf("PostNote: %v", err)
	}
	if !strings.Contains(out.String(), "replacing the note already on it") {
		t.Errorf("the confirmation does not mention the existing note:\n%v", out)
	}
}

func TestPostNoteNotOnBaseBranch(t *testing.T) {
	run := newFakeRunner(t).expect("git rev-parse --abbrev-ref HEAD", "my-feature\n", nil)
	app, _ := testApp(t, run, "")
	err := app.PostNote(t.Context(), "my-feature", true, false, false)
	if err == nil || !strings.Contains(err.Error(), "runs on the base branch main") {
		t.Errorf("got %v, want a refusal to run off the base branch", err)
	}
}

func TestPostNoteNotMerged(t *testing.T) {
	// Until it is merged there is no commit on the base branch to annotate.
	open := `{"number":7,"state":"OPEN","body":"b","headRefName":"my-feature"}`
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
		expect(viewCmd("my-feature"), open, nil)
	app, _ := testApp(t, run, "")
	err := app.PostNote(t.Context(), "my-feature", true, false, false)
	if err == nil || !strings.Contains(err.Error(), "is open") {
		t.Errorf("got %v, want an error about the state", err)
	}
	if run.ranPrefix("git notes") {
		t.Error("a note was attached for an unmerged pull request")
	}
}

func TestPostNoteEmptyBody(t *testing.T) {
	// An empty description would make an empty note, which says less than
	// no note at all.
	for _, body := range []string{"", "   \\n  "} {
		pr := `{"number":42,"state":"MERGED","body":"` + body +
			`","headRefName":"my-feature","mergeCommit":{"oid":"squash1"}}`
		run := newFakeRunner(t).
			expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
			expect(viewCmd("my-feature"), pr, nil)
		app, _ := testApp(t, run, "")
		err := app.PostNote(t.Context(), "my-feature", true, false, false)
		if err == nil || !strings.Contains(err.Error(), "no description to attach") {
			t.Errorf("%q: got %v, want an empty description error", body, err)
		}
	}
}

func TestPostNoteMergeCommitMissing(t *testing.T) {
	pr := `{"number":42,"state":"MERGED","body":"b","headRefName":"my-feature","mergeCommit":null}`
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
		expect(viewCmd("my-feature"), pr, nil)
	app, _ := testApp(t, run, "")
	err := app.PostNote(t.Context(), "my-feature", true, false, false)
	if err == nil || !strings.Contains(err.Error(), "no merge commit") {
		t.Errorf("got %v, want an error about the missing merge commit", err)
	}
}

func TestPostNoteDeletesIdenticalBranch(t *testing.T) {
	// The branch holds the content that was merged, so it has served its
	// purpose and can go.
	run := newFakeRunner(t)
	mergedNoteState(run, "a summary").
		expect("git rev-parse my-feature^{tree}", "sametree\n", nil).
		expect("git rev-parse squash1234567890^{tree}", "sametree\n", nil).
		expect("git branch --delete my-feature", "", nil)

	app, out := testApp(t, run, "y\n")
	if err := app.PostNote(t.Context(), "my-feature", false, true, false); err != nil {
		t.Fatalf("PostNote: %v", err)
	}
	if !run.ran("git branch --delete my-feature") {
		t.Errorf("the branch was not deleted: %v", run.ranCommands())
	}
	if !strings.Contains(out.String(), "deleted the local branch my-feature") {
		t.Errorf("the output does not confirm the delete:\n%v", out)
	}
	if run.ranPrefix("git push origin --delete") {
		t.Error("the branch was deleted from origin without --delete-remote")
	}
	run.assertNoUnexpected()
}

func TestPostNoteKeepsDifferentBranch(t *testing.T) {
	// The content differs from what was merged, so the branch holds
	// something that would be lost. The note is still attached.
	run := newFakeRunner(t)
	mergedNoteState(run, "a summary").
		expect("git rev-parse my-feature^{tree}", "branchtree\n", nil).
		expect("git rev-parse squash1234567890^{tree}", "mergedtree\n", nil)

	app, out := testApp(t, run, "y\n")
	if err := app.PostNote(t.Context(), "my-feature", false, true, false); err != nil {
		t.Fatalf("got %v, want no error: not deleting is not a failure", err)
	}
	if run.ranPrefix("git branch --delete") {
		t.Error("a branch whose content was not merged was deleted")
	}
	for _, want := range []string{"not deleting my-feature", "branchtr", "mergedtr"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output does not contain %q:\n%v", want, out)
		}
	}
	run.assertNoUnexpected()
}

func TestPostNoteDeletesRemoteBranch(t *testing.T) {
	run := newFakeRunner(t)
	mergedNoteState(run, "a summary").
		expect("git rev-parse my-feature^{tree}", "sametree\n", nil).
		expect("git rev-parse squash1234567890^{tree}", "sametree\n", nil).
		expect("git branch --delete my-feature", "", nil).
		expect("git ls-remote --heads origin my-feature", "sha\trefs/heads/my-feature\n", nil).
		expect("git push origin --delete my-feature", "", nil)

	app, out := testApp(t, run, "y\n")
	if err := app.PostNote(t.Context(), "my-feature", false, true, true); err != nil {
		t.Fatalf("PostNote: %v", err)
	}
	if !run.ran("git push origin --delete my-feature") {
		t.Errorf("the branch was not deleted from origin: %v", run.ranCommands())
	}
	if !strings.Contains(out.String(), "deleted my-feature from origin") {
		t.Errorf("the output does not confirm the remote delete:\n%v", out)
	}
	run.assertNoUnexpected()
}

func TestPostNoteRemoteBranchAlreadyGone(t *testing.T) {
	// GitHub deletes the branch on merge when it is configured to, which is
	// not a failure to delete it.
	run := newFakeRunner(t)
	mergedNoteState(run, "a summary").
		expect("git rev-parse my-feature^{tree}", "sametree\n", nil).
		expect("git rev-parse squash1234567890^{tree}", "sametree\n", nil).
		expect("git branch --delete my-feature", "", nil).
		expect("git ls-remote --heads origin my-feature", "", nil)

	app, out := testApp(t, run, "y\n")
	if err := app.PostNote(t.Context(), "my-feature", false, true, true); err != nil {
		t.Fatalf("PostNote: %v", err)
	}
	if run.ranPrefix("git push origin --delete") {
		t.Error("a branch that is not on origin was deleted from it")
	}
	if !strings.Contains(out.String(), "not on origin") {
		t.Errorf("the output does not say so:\n%v", out)
	}
	run.assertNoUnexpected()
}

func TestPostNoteFetchFails(t *testing.T) {
	// The merge commit is made by github, so without a fetch it is not here
	// and the comparison below it would be made against a stale branch.
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
		expect(viewCmd("my-feature"), prJSON, nil).
		expect("git fetch origin main", "", errors.New("could not read from remote"))
	app, _ := testApp(t, run, "")
	err := app.PostNote(t.Context(), "my-feature", true, false, false)
	if err == nil || !strings.Contains(err.Error(), "cannot fetch main") {
		t.Errorf("got %v, want the fetch failure", err)
	}
}

func TestPostNoteDeclined(t *testing.T) {
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
		expect(viewCmd("my-feature"), prJSON, nil).
		expect("git fetch origin main", "", nil).
		expect("git rev-parse squash1234567890", "squash1234567890\n", nil).
		expect("git notes show squash1234567890", "", errors.New("no note"))

	app, _ := testApp(t, run, "n\n")
	if err := app.PostNote(t.Context(), "my-feature", true, true, false); err != nil {
		t.Fatalf("got %v, want no error for a declined action", err)
	}
	for _, prefix := range []string{"git notes add", "git push", "git branch --delete"} {
		if run.ranPrefix(prefix) {
			t.Errorf("%v ran after being declined", prefix)
		}
	}
}

func TestConfirm(t *testing.T) {
	for _, tc := range []struct {
		answer string
		want   bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{" y \n", true},
		{"n\n", false},
		{"\n", false},
		{"anything\n", false},
		// End of input is not consent.
		{"", false},
	} {
		app, _ := testApp(t, newFakeRunner(t), tc.answer)
		got, err := app.confirm("go ahead?")
		if err != nil && tc.answer != "" {
			t.Errorf("%q: %v", tc.answer, err)
		}
		if got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.answer, got, tc.want)
		}
	}

	// --yes and a dry run both answer for the caller, and neither reads the
	// input, so neither can block waiting for one.
	for _, tc := range []struct {
		name string
		set  func(*App)
	}{
		{"--yes", func(a *App) { a.AssumeYes = true }},
		{"--dry-run", func(a *App) { a.DryRun = true }},
	} {
		app, out := testApp(t, newFakeRunner(t), "")
		tc.set(app)
		got, err := app.confirm("go ahead?")
		if err != nil || !got {
			t.Errorf("%v: got %v, %v, want true", tc.name, got, err)
		}
		if strings.Contains(out.String(), "go ahead?") {
			t.Errorf("%v: asked anyway:\n%v", tc.name, out)
		}
	}
}

func TestShortSHA(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"squash1234567890", "squash12"},
		{"short", "short"},
		{"", ""},
		{"12345678", "12345678"},
	} {
		if got := shortSHA(tc.in); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func openPRJSON(title, body string) string {
	return openPRWithChecksJSON(title, body, "[]")
}

func openPRWithChecksJSON(title, body, checksJSON string) string {
	return `{"number":42,"state":"OPEN","title":` + quoteJSON(title) + `,"body":` + quoteJSON(body) +
		`,"url":"u","headRefName":"my-feature","headRefOid":"feature1","mergeCommit":null,"statusCheckRollup":` + checksJSON + `}`
}

func mergedPRJSON(title, body string) string {
	return `{"number":42,"state":"MERGED","title":` + quoteJSON(title) + `,"body":` + quoteJSON(body) +
		`,"url":"u","headRefName":"my-feature","headRefOid":"feature1","mergeCommit":{"oid":"squash1234567890"}}`
}

func TestMergeWithSummaryOnBaseBranch(t *testing.T) {
	body := "## Overview\n\nUses **claude** and `agy` \u2014 see [docs](d)."
	mergeCmd := `gh pr merge 42 --squash --subject "feat: add summary (#42)" --body-file - --match-head-commit feature1`
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
		expect(viewCmd("my-feature"), openPRJSON("feat: add summary", body), nil).
		expect(mergeCmd, "", nil).
		expect("git fetch origin main", "", nil).
		expect("git merge --ff-only origin/main", "", nil)

	app, out := testApp(t, run, "y\n")
	if err := app.MergeWithSummary(t.Context(), "my-feature", true, false, false); err != nil {
		t.Fatalf("MergeWithSummary: %v", err)
	}

	gotBody := run.stdinFor("gh pr merge")
	wantBody := "--- Overview\n\nUses claude and agy -- see docs (d)."
	if gotBody != wantBody {
		t.Errorf("got commit body %q, want %q", gotBody, wantBody)
	}
	if !isASCII(gotBody) {
		t.Errorf("commit body is not ascii: %q", gotBody)
	}
	for _, msg := range []string{"feat: add summary (#42)", "squash-merged #42 (my-feature) into main", "updated local main from origin"} {
		if !strings.Contains(out.String(), msg) {
			t.Errorf("output missing %q:\n%v", msg, out)
		}
	}
	run.assertNoUnexpected()
}

func TestMergeWithSummaryOnFeatureBranch(t *testing.T) {
	mergeCmd := `gh pr merge 42 --squash --subject "fix: bug (#42)" --body-file - --match-head-commit feature1`
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "my-feature\n", nil).
		expect(viewCmd("my-feature"), openPRJSON("fix: bug", "fixed the bug"), nil).
		expect(mergeCmd, "", nil).
		expect("git fetch origin main:main", "", nil)

	app, _ := testApp(t, run, "y\n")
	if err := app.MergeWithSummary(t.Context(), "", true, false, false); err != nil {
		t.Fatalf("MergeWithSummary: %v", err)
	}
	if got, want := run.stdinFor("gh pr merge"), "fixed the bug"; got != want {
		t.Errorf("got commit body %q, want %q", got, want)
	}
	run.assertNoUnexpected()
}

func TestMergeWithSummaryChecksPassed(t *testing.T) {
	checks := `[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"},{"__typename":"StatusContext","context":"ci/lint","state":"SUCCESS"}]`
	mergeCmd := `gh pr merge 42 --squash --subject "feat: checks (#42)" --body-file - --match-head-commit feature1`
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
		expect(viewCmd("my-feature"), openPRWithChecksJSON("feat: checks", "summary text", checks), nil).
		expect(mergeCmd, "", nil).
		expect("git fetch origin main", "", nil).
		expect("git merge --ff-only origin/main", "", nil)

	app, out := testApp(t, run, "y\n")
	if err := app.MergeWithSummary(t.Context(), "my-feature", true, false, false); err != nil {
		t.Fatalf("MergeWithSummary: %v", err)
	}
	if !strings.Contains(out.String(), "all 2 status check(s) passed for #42") {
		t.Errorf("output missing status check confirmation:\n%v", out)
	}
	run.assertNoUnexpected()
}

func TestMergeWithSummaryBypassChecks(t *testing.T) {
	checks := `[{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"FAILURE"}]`
	mergeCmd := `gh pr merge 42 --squash --subject "feat: bypass (#42)" --body-file - --match-head-commit feature1`
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
		expect(viewCmd("my-feature"), openPRWithChecksJSON("feat: bypass", "summary text", checks), nil).
		expect(mergeCmd, "", nil).
		expect("git fetch origin main", "", nil).
		expect("git merge --ff-only origin/main", "", nil)

	app, _ := testApp(t, run, "y\n")
	if err := app.MergeWithSummary(t.Context(), "my-feature", false, false, false); err != nil {
		t.Fatalf("MergeWithSummary: %v", err)
	}
	run.assertNoUnexpected()
}

func TestMergeWithSummaryByNumberWithDeleteRemote(t *testing.T) {
	mergeCmd := `gh pr merge 42 --squash --subject "feat: add summary (#42)" --body-file - --match-head-commit feature1`
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
		expectSeq(viewCmd("42"),
			fakeResult{out: openPRJSON("feat: add summary", "summary text")},
			fakeResult{out: mergedPRJSON("feat: add summary", "summary text")},
		).
		expect(mergeCmd, "", nil).
		expect("git fetch origin main", "", nil).
		expect("git merge --ff-only origin/main", "", nil).
		expect("git rev-parse my-feature^{tree}", "sametree1\n", nil).
		expect("git rev-parse squash1234567890^{tree}", "sametree1\n", nil).
		expect("git branch --delete my-feature", "", nil).
		expect("git ls-remote --heads origin my-feature", "feature1\trefs/heads/my-feature\n", nil).
		expect("git push origin --delete my-feature", "", nil)

	app, out := testApp(t, run, "y\n")
	if err := app.MergeWithSummary(t.Context(), "42", true, true, true); err != nil {
		t.Fatalf("MergeWithSummary: %v", err)
	}
	for _, msg := range []string{"deleted the local branch my-feature", "deleted my-feature from origin"} {
		if !strings.Contains(out.String(), msg) {
			t.Errorf("output missing %q:\n%v", msg, out)
		}
	}
	run.assertNoUnexpected()
}

func TestMergeWithSummaryDeleteBranchFallbackRevParse(t *testing.T) {
	mergeCmd := `gh pr merge 42 --squash --subject "feat: add summary (#42)" --body-file - --match-head-commit feature1`
	// Merged PR without mergeCommit populated falls back to rev-parse on base.
	noMergeCommitPR := `{"number":42,"state":"MERGED","title":"feat: add summary","body":"summary text","url":"u","headRefName":"my-feature","headRefOid":"feature1","mergeCommit":null}`
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
		expect(viewCmd("my-feature"), openPRJSON("feat: add summary", "summary text"), nil).
		expect(mergeCmd, "", nil).
		expect("git fetch origin main", "", nil).
		expect("git merge --ff-only origin/main", "", nil).
		expect(viewCmd("42"), noMergeCommitPR, nil).
		expect("git rev-parse main", "basesha99\n", nil).
		expect("git rev-parse my-feature^{tree}", "sametree1\n", nil).
		expect("git rev-parse basesha99^{tree}", "sametree1\n", nil).
		expect("git branch --delete my-feature", "", nil)

	app, _ := testApp(t, run, "y\n")
	if err := app.MergeWithSummary(t.Context(), "my-feature", true, true, false); err != nil {
		t.Fatalf("MergeWithSummary: %v", err)
	}
	run.assertNoUnexpected()
}

func TestMergeWithSummaryErrors(t *testing.T) {
	t.Run("on base without target", func(t *testing.T) {
		run := newFakeRunner(t).expect("git rev-parse --abbrev-ref HEAD", "main\n", nil)
		app, _ := testApp(t, run, "")
		err := app.MergeWithSummary(t.Context(), "", true, false, false)
		if err == nil || !strings.Contains(err.Error(), "needs a branch or pull request number") {
			t.Errorf("got %v, want target-required error", err)
		}
	})

	t.Run("not open", func(t *testing.T) {
		run := newFakeRunner(t).
			expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
			expect(viewCmd("my-feature"), mergedPRJSON("t", "body"), nil)
		app, _ := testApp(t, run, "")
		err := app.MergeWithSummary(t.Context(), "my-feature", true, false, false)
		if err == nil || !strings.Contains(err.Error(), "requires an open pull request") {
			t.Errorf("got %v, want open-PR error", err)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		run := newFakeRunner(t).
			expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
			expect(viewCmd("my-feature"), openPRJSON("t", "   \n"), nil)
		app, _ := testApp(t, run, "")
		err := app.MergeWithSummary(t.Context(), "my-feature", true, false, false)
		if err == nil || !strings.Contains(err.Error(), "empty description") {
			t.Errorf("got %v, want empty-description error", err)
		}
	})

	t.Run("failing and pending status checks", func(t *testing.T) {
		checks := `[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE"},{"__typename":"StatusContext","context":"ci/lint","state":"PENDING"}]`
		run := newFakeRunner(t).
			expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
			expect(viewCmd("my-feature"), openPRWithChecksJSON("t", "body", checks), nil)
		app, _ := testApp(t, run, "y\n")
		err := app.MergeWithSummary(t.Context(), "my-feature", true, false, false)
		if err == nil || !strings.Contains(err.Error(), "test (failure)") || !strings.Contains(err.Error(), "ci/lint (pending)") || !strings.Contains(err.Error(), "--check-status=false") {
			t.Errorf("got %v, want status check failure/pending error", err)
		}
		if run.ranPrefix("gh pr merge") {
			t.Error("merged even though status checks failed/pending")
		}
	})

	t.Run("delete checked out branch refused", func(t *testing.T) {
		run := newFakeRunner(t).
			expect("git rev-parse --abbrev-ref HEAD", "my-feature\n", nil).
			expect(viewCmd("my-feature"), openPRJSON("t", "body"), nil)
		app, _ := testApp(t, run, "y\n")
		err := app.MergeWithSummary(t.Context(), "", true, true, false)
		if err == nil || !strings.Contains(err.Error(), "cannot delete my-feature while it is checked out") {
			t.Errorf("got %v, want checked-out error", err)
		}
		if run.ranPrefix("gh pr merge") {
			t.Error("merged even though delete-branch check failed")
		}
	})

	t.Run("declined", func(t *testing.T) {
		run := newFakeRunner(t).
			expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
			expect(viewCmd("my-feature"), openPRJSON("t", "body"), nil)
		app, _ := testApp(t, run, "n\n")
		if err := app.MergeWithSummary(t.Context(), "my-feature", true, false, false); err != nil {
			t.Fatalf("MergeWithSummary: %v", err)
		}
		if run.ranPrefix("gh pr merge") {
			t.Error("merged after user declined")
		}
	})

	t.Run("ff-only failure after merge", func(t *testing.T) {
		mergeCmd := `gh pr merge 42 --squash --subject "t (#42)" --body-file - --match-head-commit feature1`
		run := newFakeRunner(t).
			expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
			expect(viewCmd("my-feature"), openPRJSON("t", "body"), nil).
			expect(mergeCmd, "", nil).
			expect("git fetch origin main", "", nil).
			expect("git merge --ff-only origin/main", "", errors.New("not possible to fast-forward"))
		app, _ := testApp(t, run, "y\n")
		err := app.MergeWithSummary(t.Context(), "my-feature", true, false, false)
		if err == nil || !strings.Contains(err.Error(), "failed to fast-forward main") {
			t.Errorf("got %v, want fast-forward error", err)
		}
	})
}

func TestMergeWithSummaryDryRun(t *testing.T) {
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
		expect(viewCmd("my-feature"), openPRJSON("feat: dry", "body text"), nil)

	app, out := testApp(t, run, "")
	announce := func(cmd string) { app.printf("would run: %v\n", cmd) }
	app.DryRun = true
	app.Git = app.Git.DryRun(announce)
	app.GH = app.GH.DryRun(announce)
	if err := app.MergeWithSummary(t.Context(), "my-feature", true, true, true); err != nil {
		t.Fatalf("MergeWithSummary dry run: %v", err)
	}
	if run.ranPrefix("gh pr merge") || run.ranPrefix("git fetch") || run.ranPrefix("git branch") {
		t.Errorf("dry run ran unexpected commands: %v", run.ranCommands())
	}
	for _, want := range []string{
		`gh pr merge 42 --squash --subject "feat: dry (#42)" --body-file - --match-head-commit feature1`,
		"git merge --ff-only origin/main",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run output missing %q:\n%v", want, out)
		}
	}
	run.assertNoUnexpected()
}

func TestWriteNotesAsCommits(t *testing.T) {
	c1Raw := "tree tree111\nparent base000\nauthor A <a@b> 100 +0000\ncommitter C <c@d> 100 +0000\n\nfeat: squash (#42)\n\n* wip 1\n* wip 2\n"
	c2Raw := "tree tree222\nparent targetsha1\nauthor B <b@b> 200 +0000\ncommitter B <b@b> 200 +0000\n\nchore: later\n\nlater body\n"

	t.Run("local only rewrites target and descendants", func(t *testing.T) {
		run := newFakeRunner(t).
			expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
			expect("git status --porcelain", "", nil).
			expect("git rev-parse abc123^{commit}", "targetsha1\n", nil).
			expect("git rev-parse HEAD", "headsha2\n", nil).
			expect("git merge-base targetsha1 HEAD", "targetsha1\n", nil).
			expect("git notes show targetsha1", "summary from note\n", nil).
			expect("git cat-file commit targetsha1", c1Raw, nil).
			expect("git rev-list --ancestry-path --reverse --topo-order targetsha1..HEAD", "headsha2\n", nil).
			expect("git commit-tree tree111 -p base000 -F -", "newtarget1\n", nil).
			expect("git cat-file commit headsha2", c2Raw, nil).
			expect("git commit-tree tree222 -p newtarget1 -F -", "newhead2\n", nil).
			expect("git reset --keep newhead2", "", nil)

		app, out := testApp(t, run, "y\n")
		if err := app.WriteNotesAsCommits(t.Context(), "abc123", false); err != nil {
			t.Fatalf("WriteNotesAsCommits: %v", err)
		}
		if run.ranPrefix("git push") {
			t.Error("local-only run pushed to origin")
		}
		if got := run.stdinFor("git commit-tree tree111"); got != "feat: squash (#42)\n\nsummary from note\n" {
			t.Errorf("target commit message = %q", got)
		}
		if got := run.stdinFor("git commit-tree tree222"); got != "chore: later\n\nlater body\n" {
			t.Errorf("descendant commit message = %q", got)
		}
		if !strings.Contains(out.String(), "updated main to newhead2 (previous tip was headsha2") {
			t.Errorf("unexpected output:\n%v", out)
		}
		run.assertNoUnexpected()
	})

	t.Run("with push", func(t *testing.T) {
		run := newFakeRunner(t).
			expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
			expect("git status --porcelain", "", nil).
			expect("git rev-parse HEAD^{commit}", "targetsha1\n", nil).
			expect("git rev-parse HEAD", "targetsha1\n", nil).
			expect("git merge-base targetsha1 HEAD", "targetsha1\n", nil).
			expect("git notes show targetsha1", "summary from note\n", nil).
			expect("git cat-file commit targetsha1", c1Raw, nil).
			expect("git commit-tree tree111 -p base000 -F -", "newtarget1\n", nil).
			expect("git reset --keep newtarget1", "", nil).
			expect("git push --force-with-lease origin main", "", nil)

		app, out := testApp(t, run, "y\n")
		if err := app.WriteNotesAsCommits(t.Context(), "HEAD", true); err != nil {
			t.Fatalf("WriteNotesAsCommits --push: %v", err)
		}
		if !strings.Contains(out.String(), "pushed main to origin with --force-with-lease") {
			t.Errorf("missing push message:\n%v", out)
		}
		run.assertNoUnexpected()
	})
}

func TestWriteNotesAsCommitsErrors(t *testing.T) {
	t.Run("detached head", func(t *testing.T) {
		run := newFakeRunner(t).expect("git rev-parse --abbrev-ref HEAD", "HEAD\n", nil)
		app, _ := testApp(t, run, "")
		err := app.WriteNotesAsCommits(t.Context(), "abc", false)
		if err == nil || !strings.Contains(err.Error(), "detached HEAD") {
			t.Errorf("got %v, want detached HEAD error", err)
		}
	})

	t.Run("dirty working tree", func(t *testing.T) {
		run := newFakeRunner(t).
			expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
			expect("git status --porcelain", " M foo.go\n", nil)
		app, _ := testApp(t, run, "")
		err := app.WriteNotesAsCommits(t.Context(), "abc", false)
		if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
			t.Errorf("got %v, want dirty tree error", err)
		}
	})

	t.Run("not ancestor", func(t *testing.T) {
		run := newFakeRunner(t).
			expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
			expect("git status --porcelain", "", nil).
			expect("git rev-parse other^{commit}", "othersha1\n", nil).
			expect("git rev-parse HEAD", "headsha1\n", nil).
			expect("git merge-base othersha1 HEAD", "common0\n", nil)
		app, _ := testApp(t, run, "")
		err := app.WriteNotesAsCommits(t.Context(), "other", false)
		if err == nil || !strings.Contains(err.Error(), "not an ancestor") {
			t.Errorf("got %v, want non-ancestor error", err)
		}
	})

	t.Run("no git note", func(t *testing.T) {
		run := newFakeRunner(t).
			expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
			expect("git status --porcelain", "", nil).
			expect("git rev-parse abc^{commit}", "targetsha1\n", nil).
			expect("git rev-parse HEAD", "targetsha1\n", nil).
			expect("git merge-base targetsha1 HEAD", "targetsha1\n", nil).
			expect("git notes show targetsha1", "", errors.New("no note found"))
		app, _ := testApp(t, run, "")
		err := app.WriteNotesAsCommits(t.Context(), "abc", false)
		if err == nil || !strings.Contains(err.Error(), "has no git note") {
			t.Errorf("got %v, want missing note error", err)
		}
	})
}

func TestWriteNotesAsCommitsDryRun(t *testing.T) {
	c1Raw := "tree tree111\nparent base000\nauthor A <a@b> 100 +0000\ncommitter C <c@d> 100 +0000\n\nfeat: squash (#42)\n"
	c2Raw := "tree tree222\nparent targetsha1\nauthor B <b@b> 200 +0000\ncommitter B <b@b> 200 +0000\n\nchore: later\n"
	run := newFakeRunner(t).
		expect("git rev-parse --abbrev-ref HEAD", "main\n", nil).
		expect("git status --porcelain", "", nil).
		expect("git rev-parse abc123^{commit}", "targetsha1\n", nil).
		expect("git rev-parse HEAD", "headsha2\n", nil).
		expect("git merge-base targetsha1 HEAD", "targetsha1\n", nil).
		expect("git notes show targetsha1", "note body", nil).
		expect("git cat-file commit targetsha1", c1Raw, nil).
		expect("git rev-list --ancestry-path --reverse --topo-order targetsha1..HEAD", "headsha2\n", nil).
		expect("git cat-file commit headsha2", c2Raw, nil)

	app, out := testApp(t, run, "")
	announce := func(cmd string) { app.printf("would run: %v\n", cmd) }
	app.DryRun = true
	app.Git = app.Git.DryRun(announce)
	app.GH = app.GH.DryRun(announce)

	if err := app.WriteNotesAsCommits(t.Context(), "abc123", true); err != nil {
		t.Fatalf("WriteNotesAsCommits dry run: %v", err)
	}
	if run.ranPrefix("git commit-tree") || run.ranPrefix("git reset") || run.ranPrefix("git push") {
		t.Errorf("dry run ran mutating commands: %v", run.ranCommands())
	}
	for _, want := range []string{
		"git commit-tree tree111 -p base000 -F -",
		"git commit-tree tree222 -p <rewritten-targetsh> -F -",
		"git reset --keep <rewritten-headsha2>",
		"git push --force-with-lease origin main",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run output missing %q:\n%v", want, out)
		}
	}
	run.assertNoUnexpected()
}

type dirRunner struct {
	dir string
}

func (d dirRunner) Run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	return d.RunEnv(ctx, nil, stdin, name, args...)
}

func (d dirRunner) RunEnv(ctx context.Context, env []string, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = d.dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), errors.New(strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func TestWriteNotesAsCommitsRealRepo(t *testing.T) {
	dir := t.TempDir()
	r := dirRunner{dir: dir}
	gitRun := func(env []string, args ...string) string {
		t.Helper()
		out, err := r.RunEnv(t.Context(), env, nil, "git", args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}

	gitRun(nil, "init", "-b", "main")
	gitRun(nil, "config", "user.name", "Test User")
	gitRun(nil, "config", "user.email", "test@example.com")

	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("a.txt", "one\n")
	gitRun(nil, "add", "a.txt")
	gitRun([]string{"GIT_AUTHOR_DATE=1700000000 +0000", "GIT_COMMITTER_DATE=1700000000 +0000"}, "commit", "-m", "init")
	c1 := gitRun(nil, "rev-parse", "HEAD")

	write("b.txt", "two\n")
	gitRun(nil, "add", "b.txt")
	gitRun([]string{
		"GIT_AUTHOR_NAME=Original Author",
		"GIT_AUTHOR_EMAIL=orig@example.com",
		"GIT_AUTHOR_DATE=1700000100 -0800",
		"GIT_COMMITTER_DATE=1700000200 -0800",
	}, "commit", "-m", "feat: squash (#10)", "-m", "* wip commit 1\n* wip commit 2")
	c2 := gitRun(nil, "rev-parse", "HEAD")
	gitRun(nil, "notes", "add", "-m", "Detailed ASCII note body.", c2)

	write("c.txt", "three\n")
	gitRun(nil, "add", "c.txt")
	gitRun([]string{"GIT_AUTHOR_DATE=1700000300 +0000", "GIT_COMMITTER_DATE=1700000300 +0000"}, "commit", "-m", "chore: follow-up", "-m", "Keep this body.")
	c3 := gitRun(nil, "rev-parse", "HEAD")
	c3Tree := gitRun(nil, "rev-parse", c3+"^{tree}")

	var out bytes.Buffer
	app := &App{
		Git:    NewGit(r),
		GH:     NewGH(r),
		Base:   "main",
		Stdout: &out,
		Stdin:  strings.NewReader("y\n"),
	}
	if err := app.WriteNotesAsCommits(t.Context(), c2, false); err != nil {
		t.Fatalf("WriteNotesAsCommits: %v", err)
	}

	newC3 := gitRun(nil, "rev-parse", "HEAD")
	newC2 := gitRun(nil, "rev-parse", "HEAD~1")
	if got := gitRun(nil, "rev-parse", "HEAD~2"); got != c1 {
		t.Errorf("ancestor commit changed: got %s, want %s", got, c1)
	}
	if got := gitRun(nil, "rev-parse", newC3+"^{tree}"); got != c3Tree {
		t.Errorf("tree changed: got %s, want %s", got, c3Tree)
	}
	if got := gitRun(nil, "log", "-1", "--format=%B", newC2); got != "feat: squash (#10)\n\nDetailed ASCII note body." {
		t.Errorf("rewritten target message = %q", got)
	}
	if got := gitRun(nil, "log", "-1", "--format=%an|%ae|%at", newC2); got != "Original Author|orig@example.com|1700000100" {
		t.Errorf("rewritten target author/date = %q", got)
	}
	if got := gitRun(nil, "log", "-1", "--format=%B", newC3); got != "chore: follow-up\n\nKeep this body." {
		t.Errorf("descendant message = %q", got)
	}
	if dirty := gitRun(nil, "status", "--porcelain"); dirty != "" {
		t.Errorf("working tree dirty after rewrite: %q", dirty)
	}
	if slices.Equal([]string{c2, c3}, []string{newC2, newC3}) {
		t.Error("expected rewritten commits to have new SHAs")
	}
}

func TestListCommitsWithNotes(t *testing.T) {
	t.Run("filters unreachable and keeps newest first", func(t *testing.T) {
		run := newFakeRunner(t).
			expect("git notes list", "n1 sha1\nn2 sha3\nn3 orphan\n", nil).
			expect("git log --format=%H%x09%s HEAD", "sha3\tthird\nsha2\tsecond\nsha1\tfirst\n", nil)

		app, out := testApp(t, run, "")
		if err := app.ListCommitsWithNotes(t.Context(), ""); err != nil {
			t.Fatalf("ListCommitsWithNotes: %v", err)
		}
		want := "sha3 third\nsha1 first\n"
		if out.String() != want {
			t.Errorf("got %q, want %q", out.String(), want)
		}
		run.assertNoUnexpected()
	})

	t.Run("no notes skips log", func(t *testing.T) {
		run := newFakeRunner(t).expect("git notes list", "", nil)
		app, out := testApp(t, run, "")
		if err := app.ListCommitsWithNotes(t.Context(), "main"); err != nil {
			t.Fatalf("ListCommitsWithNotes: %v", err)
		}
		if out.String() != "" {
			t.Errorf("got %q, want empty output", out.String())
		}
		run.assertNoUnexpected()
	})
}

func TestListAndRewriteMultipleNotesRealRepo(t *testing.T) {
	dir := t.TempDir()
	r := dirRunner{dir: dir}
	gitRun := func(args ...string) string {
		t.Helper()
		out, err := r.RunEnv(t.Context(), nil, nil, "git", args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}

	gitRun("init", "-b", "main")
	gitRun("config", "user.name", "Test User")
	gitRun("config", "user.email", "test@example.com")

	commit := func(file, content, msg string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		gitRun("add", file)
		gitRun("commit", "-m", msg)
		return gitRun("rev-parse", "HEAD")
	}

	c1 := commit("a.txt", "1\n", "first (#1)")
	c2 := commit("b.txt", "2\n", "second (#2)")
	c3 := commit("c.txt", "3\n", "third (#3)")
	gitRun("notes", "add", "-m", "note one", c1)
	gitRun("notes", "add", "-m", "note three", c3)
	_ = c2

	var out bytes.Buffer
	app := &App{
		Git:       NewGit(r),
		GH:        NewGH(r),
		Base:      "main",
		Stdout:    &out,
		Stdin:     strings.NewReader(""),
		AssumeYes: true,
	}
	if err := app.ListCommitsWithNotes(t.Context(), ""); err != nil {
		t.Fatalf("ListCommitsWithNotes: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	wantLines := []string{c3 + " third (#3)", c1 + " first (#1)"}
	if !slices.Equal(lines, wantLines) {
		t.Fatalf("candidates = %v, want %v", lines, wantLines)
	}

	// Rewrite each candidate in the listed newest-to-oldest order locally.
	for _, line := range lines {
		sha, _, _ := strings.Cut(line, " ")
		if err := app.WriteNotesAsCommits(t.Context(), sha, false); err != nil {
			t.Fatalf("WriteNotesAsCommits(%s): %v", sha, err)
		}
	}

	// Afterward no reachable commits have notes and both notes live in messages.
	out.Reset()
	if err := app.ListCommitsWithNotes(t.Context(), ""); err != nil {
		t.Fatalf("ListCommitsWithNotes after rewrite: %v", err)
	}
	if out.String() != "" {
		t.Errorf("expected no remaining candidates, got %q", out.String())
	}
	if got := gitRun("log", "-1", "--format=%B", "HEAD"); got != "third (#3)\n\nnote three" {
		t.Errorf("HEAD message = %q", got)
	}
	if got := gitRun("log", "-1", "--format=%B", "HEAD~2"); got != "first (#1)\n\nnote one" {
		t.Errorf("HEAD~2 message = %q", got)
	}
}
