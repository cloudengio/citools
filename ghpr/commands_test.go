// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"errors"
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
	onFeatureBranch(run).expect(viewCmd("my-feature"), "", errors.New(noPRErr))
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
	onFeatureBranch(run).expect(viewCmd("my-feature"), "", errors.New(noPRErr))
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
	onFeatureBranch(run).expect(viewCmd("my-feature"), "", errors.New(noPRErr))
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
