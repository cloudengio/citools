// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// App holds what the commands do their work with. Everything that touches
// the repository, GitHub or an agent is reached through it, so a test can
// supply its own.
type App struct {
	Git                *Git
	GH                 *GH
	Summarizer         *Summarizer
	Base               string
	Stdout             io.Writer
	Stdin              io.Reader
	AssumeYes          bool
	DryRun             bool
	ShowSummaryCommand bool

	// reader buffers Stdin across confirmations. It has to be kept: a
	// reader made per question buffers what it did not use and discards it,
	// so the second question would see nothing of what was typed for it.
	reader *bufio.Reader
}

func (a *App) printf(format string, args ...any) {
	// Nothing useful can be done about a failure to write progress to the
	// terminal, and reporting it there is self defeating.
	_, _ = fmt.Fprintf(a.Stdout, format, args...)
}

// show prints a generated description or note, bracketed so that where it
// starts and ends is unambiguous whatever it contains.
func (a *App) show(title, text string) {
	a.printf("\n========== %v ==========\n%v\n%v\n\n",
		title, strings.TrimRight(text, "\n"), strings.Repeat("=", len(title)+22))
}

// confirm asks whether to go ahead, and reports whether it was answered yes.
// A dry run never asks, since it changes nothing, and --yes answers for the
// caller.
func (a *App) confirm(question string) (bool, error) {
	if a.DryRun || a.AssumeYes {
		return true, nil
	}
	a.printf("%v [y/N]: ", question)
	if a.reader == nil {
		a.reader = bufio.NewReader(a.Stdin)
	}
	line, err := a.reader.ReadString('\n')
	if err != nil && line == "" {
		return false, fmt.Errorf("cannot read the answer: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	a.printf("aborted\n")
	return false, nil
}

// CreatePR creates a pull request for the current branch, with a description
// generated from what the branch changes. Any uncommitted changes on the branch
// are staged and committed with title before pushing and creating the pull
// request.
func (a *App) CreatePR(ctx context.Context, title string, draft, push bool) error {
	branch, err := a.Git.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	if branch == a.Base {
		return fmt.Errorf("create-pr runs on a feature branch, and %v is the base branch", branch)
	}
	// An existing pull request is updated rather than replaced, and saying
	// so is more use than whatever gh would report for a second one.
	switch existing, err := a.GH.View(ctx, branch); {
	case err == nil:
		return fmt.Errorf("pull request #%v already exists for %v (%v): use update-summary to change its description",
			existing.Number, branch, existing.URL)
	case !errors.Is(err, ErrNoPR):
		return err
	}

	if a.ShowSummaryCommand {
		cmd, err := a.Summarizer.Command(ctx, a.Base, branch)
		if err != nil {
			return err
		}
		a.printf("%v\n", cmd)
		return nil
	}

	committed, err := a.commitUncommitted(ctx, branch, title)
	if err != nil {
		return err
	}

	if err := a.ensurePushed(ctx, branch, push, committed); err != nil {
		return err
	}

	summary, err := a.Summarizer.Summarize(ctx, a.Base, branch)
	if err != nil {
		return err
	}
	a.show("generated description", summary)

	kind := "pull request"
	if draft {
		kind = "draft pull request"
	}
	ok, err := a.confirm(fmt.Sprintf("create a %v for %v into %v titled %q?", kind, branch, a.Base, title))
	if err != nil || !ok {
		return err
	}
	url, err := a.GH.Create(ctx, a.Base, branch, title, summary, draft)
	if err != nil {
		return err
	}
	if url != "" {
		a.printf("created %v\n", url)
	}
	return nil
}

// commitUncommitted stages and commits any uncommitted changes (including
// untracked files) on branch with title, reporting whether a commit was
// created.
func (a *App) commitUncommitted(ctx context.Context, branch, title string) (bool, error) {
	status, err := a.Git.StatusShort(ctx)
	if err != nil {
		return false, err
	}
	if status == "" {
		return false, nil
	}
	if strings.TrimSpace(title) == "" {
		return false, fmt.Errorf("cannot commit uncommitted changes with an empty title")
	}
	a.show("uncommitted changes on "+branch, status)
	ok, err := a.confirm(fmt.Sprintf("commit uncommitted changes on %v with message %q?", branch, title))
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("uncommitted changes on %v must be committed before creating a pull request", branch)
		}
		return false, err
	}
	if err := a.Git.StageAll(ctx); err != nil {
		return false, err
	}
	if err := a.Git.Commit(ctx, title); err != nil {
		return false, err
	}
	a.printf("committed changes on %v with message %q\n", branch, title)
	return true, nil
}

// ensurePushed makes sure the branch (including any newly created commit) is on
// origin, which GitHub requires before a pull request can refer to it.
func (a *App) ensurePushed(ctx context.Context, branch string, push, committed bool) error {
	onOrigin, err := a.Git.RemoteBranchExists(ctx, branch)
	if err != nil {
		return err
	}
	if onOrigin && !committed {
		return nil
	}
	if !push {
		if onOrigin {
			return fmt.Errorf("%v has new commits that are not on origin: push them, or drop --push=false", branch)
		}
		return fmt.Errorf("%v is not on origin, which github needs it to be: push it, or drop --push=false", branch)
	}
	prompt := fmt.Sprintf("%v is not on origin yet, push it?", branch)
	if onOrigin {
		prompt = fmt.Sprintf("push the newly committed changes on %v to origin?", branch)
	}
	ok, err := a.confirm(prompt)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("%v must be on origin before a pull request can be created for it", branch)
		}
		return err
	}
	return a.Git.PushBranch(ctx, branch)
}

// UpdateSummary regenerates the description of an existing pull request from
// what its branch now changes.
func (a *App) UpdateSummary(ctx context.Context, target string) error {
	if target == "" {
		branch, err := a.Git.CurrentBranch(ctx)
		if err != nil {
			return err
		}
		if branch == a.Base {
			return fmt.Errorf("on the base branch %v: name the branch or the pull request to update", a.Base)
		}
		target = branch
	}
	pr, err := a.GH.View(ctx, target)
	if err != nil {
		return err
	}

	// A merged pull request has no branch left to summarize, so its head
	// commit stands in for one. It may not be here, since the branch it was
	// on is usually deleted by then.
	ref := pr.HeadRefName
	if pr.Merged() {
		ref = pr.HeadRefOid
		//nolint:errcheck // Best effort: if the commit is here already, or
		// cannot be fetched, resolving it below says so more clearly.
		_ = a.Git.Fetch(ctx, pr.HeadRefOid)
		if _, err := a.Git.RevParse(ctx, ref); err != nil {
			return fmt.Errorf("pull request #%v is merged and its head commit %v is not in this repository: %w",
				pr.Number, shortSHA(ref), err)
		}
	}

	if a.ShowSummaryCommand {
		cmd, err := a.Summarizer.Command(ctx, a.Base, ref)
		if err != nil {
			return err
		}
		a.printf("%v\n", cmd)
		return nil
	}

	summary, err := a.Summarizer.Summarize(ctx, a.Base, ref)
	if err != nil {
		return err
	}
	a.show("generated description", summary)
	if strings.TrimSpace(summary) == strings.TrimSpace(pr.Body) {
		a.printf("the description of #%v is already this, nothing to do\n", pr.Number)
		return nil
	}

	ok, err := a.confirm(fmt.Sprintf("replace the description of #%v (%v)?", pr.Number, pr.URL))
	if err != nil || !ok {
		return err
	}
	if err := a.GH.UpdateBody(ctx, pr.Number, summary); err != nil {
		return err
	}
	a.printf("updated the description of #%v\n", pr.Number)
	return nil
}

// PostNote attaches the description of a merged pull request, as plain text,
// to the commit that it created on the base branch, and optionally deletes
// the branch it came from.
func (a *App) PostNote(ctx context.Context, branch string, pushNotes, deleteBranch, deleteRemote bool) error {
	current, err := a.Git.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	if current != a.Base {
		return fmt.Errorf("post-note runs on the base branch %v, and %v is checked out", a.Base, current)
	}
	pr, err := a.GH.View(ctx, branch)
	if err != nil {
		return err
	}
	if !pr.Merged() {
		return fmt.Errorf("pull request #%v for %v is %v: it has no commit on %v to annotate until it is merged",
			pr.Number, branch, strings.ToLower(pr.State), a.Base)
	}
	mergeSHA := pr.MergeSHA()
	if mergeSHA == "" {
		return fmt.Errorf("github reports no merge commit for pull request #%v", pr.Number)
	}
	if strings.TrimSpace(pr.Body) == "" {
		return fmt.Errorf("pull request #%v has no description to attach: run update-summary first", pr.Number)
	}

	// The merge commit is made by github, so it only arrives here with a
	// fetch, which is also what makes the comparison below current.
	if err := a.Git.Fetch(ctx, a.Base); err != nil {
		return fmt.Errorf("cannot fetch %v, which is where the merge commit is: %w", a.Base, err)
	}
	if _, err := a.Git.RevParse(ctx, mergeSHA); err != nil {
		return fmt.Errorf("the merge commit %v of pull request #%v is not in this repository: %w",
			shortSHA(mergeSHA), pr.Number, err)
	}

	note := MarkdownToASCII(pr.Body)
	a.show("note for "+shortSHA(mergeSHA), note)

	actions := []string{fmt.Sprintf("attach this note to %v", shortSHA(mergeSHA))}
	if existing := a.Git.Note(ctx, mergeSHA); existing != "" {
		actions[0] += ", replacing the note already on it"
	}
	if pushNotes {
		actions = append(actions, "push refs/notes/* to origin")
	}
	if deleteBranch {
		actions = append(actions, deleteBranchAction(branch, deleteRemote))
	}
	ok, err := a.confirm("about to " + strings.Join(actions, ", then ") + ". proceed?")
	if err != nil || !ok {
		return err
	}

	if err := a.Git.AddNote(ctx, mergeSHA, note); err != nil {
		return err
	}
	a.printf("attached the description of #%v to %v\n", pr.Number, shortSHA(mergeSHA))

	if pushNotes {
		if err := a.Git.PushNotes(ctx); err != nil {
			return fmt.Errorf("the note is attached locally but could not be pushed: %w", err)
		}
		a.printf("pushed refs/notes/* to origin\n")
	}
	if deleteBranch {
		return a.deleteMergedBranch(ctx, branch, mergeSHA, deleteRemote)
	}
	return nil
}

// MergeWithSummary squash-merges an open pull request using its description
// as the commit body, updates the local base branch, and optionally deletes
// the feature branch it came from. When checkStatus is true, any pending or
// failing status check prevents the merge.
func (a *App) MergeWithSummary(ctx context.Context, target string, checkStatus, deleteBranch, deleteRemote bool) error {
	current, err := a.Git.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	if target == "" {
		if current == a.Base {
			return fmt.Errorf("merge-with-summary needs a branch or pull request number when run on %v", a.Base)
		}
		target = current
	}
	pr, err := a.GH.View(ctx, target)
	if err != nil {
		return err
	}
	if pr.State != "OPEN" {
		return fmt.Errorf("#%v is %s; merge-with-summary requires an open pull request", pr.Number, strings.ToLower(pr.State))
	}
	if strings.TrimSpace(pr.Body) == "" {
		return fmt.Errorf("#%v has an empty description: run update-summary or edit it on github first", pr.Number)
	}
	summary := MarkdownToASCII(pr.Body)
	if strings.TrimSpace(summary) == "" {
		return fmt.Errorf("#%v has no text in its description after stripping markdown", pr.Number)
	}
	if checkStatus {
		if err := a.verifyChecks(pr); err != nil {
			return err
		}
	}
	if deleteBranch && current == pr.HeadRefName {
		return fmt.Errorf("cannot delete %v while it is checked out: switch to %v first", pr.HeadRefName, a.Base)
	}

	subject := fmt.Sprintf("%s (#%d)", pr.Title, pr.Number)
	a.show(fmt.Sprintf("commit summary for #%d", pr.Number), subject+"\n\n"+summary)

	actions := []string{
		fmt.Sprintf("squash-merge #%d (%v) into %v with this commit summary", pr.Number, pr.HeadRefName, a.Base),
		fmt.Sprintf("update local %v from origin", a.Base),
	}
	if deleteBranch {
		actions = append(actions, deleteBranchAction(pr.HeadRefName, deleteRemote))
	}
	ok, err := a.confirm("about to " + strings.Join(actions, ", then ") + ". proceed?")
	if err != nil || !ok {
		return err
	}

	if err := a.GH.SquashMerge(ctx, pr.Number, subject, summary, pr.HeadRefOid); err != nil {
		return err
	}
	a.printf("squash-merged #%v (%v) into %v\n", pr.Number, pr.HeadRefName, a.Base)

	if err := a.updateLocalBase(ctx, current); err != nil {
		return err
	}
	a.printf("updated local %v from origin\n", a.Base)

	if a.DryRun || !deleteBranch {
		return nil
	}
	mergedPR, err := a.GH.View(ctx, fmt.Sprintf("%d", pr.Number))
	if err != nil {
		return fmt.Errorf("squash-merged #%v, but could not fetch merge commit: %w", pr.Number, err)
	}
	mergeSHA := mergedPR.MergeSHA()
	if mergeSHA == "" {
		mergeSHA, err = a.Git.RevParse(ctx, a.Base)
		if err != nil {
			return fmt.Errorf("squash-merged #%v, but could not determine merge commit: %w", pr.Number, err)
		}
	}
	return a.deleteMergedBranch(ctx, pr.HeadRefName, mergeSHA, deleteRemote)
}

func (a *App) verifyChecks(pr *PR) error {
	pending, failed := pr.PendingOrFailedChecks()
	var problems []string
	if len(failed) > 0 {
		problems = append(problems, "failed: "+strings.Join(failed, ", "))
	}
	if len(pending) > 0 {
		problems = append(problems, "pending: "+strings.Join(pending, ", "))
	}
	if len(problems) > 0 {
		return fmt.Errorf("#%v status checks have not passed (%s); pass --check-status=false to merge anyway",
			pr.Number, strings.Join(problems, "; "))
	}
	if len(pr.StatusCheckRollup) > 0 {
		a.printf("all %d status check(s) passed for #%v\n", len(pr.StatusCheckRollup), pr.Number)
	}
	return nil
}

func (a *App) updateLocalBase(ctx context.Context, current string) error {
	if current == a.Base {
		if !a.DryRun {
			if err := a.Git.Fetch(ctx, a.Base); err != nil {
				return fmt.Errorf("squash-merged, but failed to fetch %v: %w", a.Base, err)
			}
		}
		if err := a.Git.MergeFFOnly(ctx, "origin/"+a.Base); err != nil {
			return fmt.Errorf("squash-merged, but failed to fast-forward %v: %w", a.Base, err)
		}
		return nil
	}
	if err := a.Git.FetchUpdateBranch(ctx, a.Base); err != nil {
		return fmt.Errorf("squash-merged, but failed to update local %v: %w", a.Base, err)
	}
	return nil
}

func deleteBranchAction(branch string, deleteRemote bool) string {
	what := "the local branch " + branch
	if deleteRemote {
		what = "the local and origin branch " + branch
	}
	return "delete " + what + " if its content is what was merged"
}

// deleteMergedBranch deletes the branch if the content it holds is the
// content that was merged.
//
// The test is that the branch and the merge commit have the same tree, ie.
// hold identical content, which is what a squash merge of an up to date
// branch produces. Comparing trees rather than commits is the only way to
// recognise that: a squash makes a new commit, so the branch's commits are
// nowhere in the base branch's history. The merge commit is compared against
// rather than the tip of the base branch so that anything merged after this
// pull request does not make the branch look unmerged.
func (a *App) deleteMergedBranch(ctx context.Context, branch, mergeSHA string, alsoRemote bool) error {
	branchTree, err := a.Git.TreeHash(ctx, branch)
	if err != nil {
		return err
	}
	mergedTree, err := a.Git.TreeHash(ctx, mergeSHA)
	if err != nil {
		return err
	}
	if branchTree != mergedTree {
		a.printf("not deleting %v: its content is not what was merged (tree %v, merged %v).\n",
			branch, shortSHA(branchTree), shortSHA(mergedTree))
		a.printf("  this is expected when %v moved on after the merge, or the branch was behind it.\n", a.Base)
		return nil
	}
	if err := a.Git.DeleteBranch(ctx, branch); err != nil {
		return err
	}
	a.printf("deleted the local branch %v\n", branch)
	if !alsoRemote {
		return nil
	}
	onOrigin, err := a.Git.RemoteBranchExists(ctx, branch)
	if err != nil {
		return err
	}
	if !onOrigin {
		a.printf("%v is not on origin, nothing to delete there\n", branch)
		return nil
	}
	if err := a.Git.DeleteRemoteBranch(ctx, branch); err != nil {
		return err
	}
	a.printf("deleted %v from origin\n", branch)
	return nil
}

// WriteNotesAsCommits rewrites the commit at rev to replace its commit body
// with its git note (keeping the original subject line), re-parents all
// descendants up to HEAD onto the rewritten commit, updates the checked-out
// branch, and optionally force-pushes it to origin.
func (a *App) WriteNotesAsCommits(ctx context.Context, rev string, push bool) error {
	branch, targetSHA, headSHA, targetCommit, newMsg, err := a.prepareWriteNotes(ctx, rev)
	if err != nil {
		return err
	}
	commits := []string{targetSHA}
	if targetSHA != headSHA {
		descendants, err := a.Git.RevList(ctx, targetSHA+"..HEAD")
		if err != nil {
			return err
		}
		commits = append(commits, descendants...)
	}

	a.show("commit message for "+shortSHA(targetSHA), strings.TrimRight(newMsg, "\n"))
	action := fmt.Sprintf("about to rewrite %d commit(s) on %v starting at %s", len(commits), branch, shortSHA(targetSHA))
	if push {
		action += ", then push with --force-with-lease to origin"
	}
	ok, err := a.confirm(action + ". proceed?")
	if err != nil || !ok {
		return err
	}

	newHead, err := a.rewriteCommits(ctx, commits, targetCommit, newMsg)
	if err != nil {
		return err
	}
	if err := a.Git.ResetKeep(ctx, newHead); err != nil {
		return fmt.Errorf("rewrote commit chain to %s, but failed to update %v: %w", shortSHA(newHead), branch, err)
	}
	a.printf("updated %v to %s (previous tip was %s; undo with: git reset --hard %s)\n",
		branch, shortSHA(newHead), shortSHA(headSHA), headSHA)
	if push {
		if err := a.Git.PushForceWithLease(ctx, branch); err != nil {
			return fmt.Errorf("updated local %v to %s, but push failed: %w", branch, shortSHA(newHead), err)
		}
		a.printf("pushed %v to origin with --force-with-lease\n", branch)
	}
	return nil
}

func (a *App) prepareWriteNotes(ctx context.Context, rev string) (branch, targetSHA, headSHA string, targetCommit *Commit, newMsg string, err error) {
	branch, err = a.Git.CurrentBranch(ctx)
	if err != nil {
		return "", "", "", nil, "", err
	}
	if branch == "HEAD" {
		return "", "", "", nil, "", fmt.Errorf("write-notes-as-commits requires a checked-out branch, not a detached HEAD")
	}
	dirty, err := a.Git.WorkingTreeDirty(ctx)
	if err != nil {
		return "", "", "", nil, "", err
	}
	if dirty {
		return "", "", "", nil, "", fmt.Errorf("working tree has uncommitted changes: commit or stash them before rewriting history")
	}
	targetSHA, err = a.Git.RevParse(ctx, rev+"^{commit}")
	if err != nil {
		return "", "", "", nil, "", err
	}
	headSHA, err = a.Git.RevParse(ctx, "HEAD")
	if err != nil {
		return "", "", "", nil, "", err
	}
	anc, err := a.Git.IsAncestor(ctx, targetSHA, "HEAD")
	if err != nil {
		return "", "", "", nil, "", err
	}
	if !anc {
		return "", "", "", nil, "", fmt.Errorf("%v (%s) is not an ancestor of %v", rev, shortSHA(targetSHA), branch)
	}
	note := a.Git.Note(ctx, targetSHA)
	if strings.TrimSpace(note) == "" {
		return "", "", "", nil, "", fmt.Errorf("%v (%s) has no git note (if notes live on origin, fetch them with: git fetch origin 'refs/notes/*:refs/notes/*')", rev, shortSHA(targetSHA))
	}
	targetCommit, err = a.Git.ReadCommit(ctx, targetSHA)
	if err != nil {
		return "", "", "", nil, "", err
	}
	return branch, targetSHA, headSHA, targetCommit, formatNoteCommitMessage(targetCommit.Subject(), note), nil
}

func formatNoteCommitMessage(subject, note string) string {
	body := strings.TrimSpace(note)
	if subject == "" {
		return body + "\n"
	}
	return subject + "\n\n" + body + "\n"
}

func (a *App) rewriteCommits(ctx context.Context, commits []string, targetCommit *Commit, newMsg string) (string, error) {
	remap := make(map[string]string, len(commits))
	var newHead string
	for i, oldSHA := range commits {
		c := targetCommit
		msg := newMsg
		if i > 0 {
			var err error
			c, err = a.Git.ReadCommit(ctx, oldSHA)
			if err != nil {
				return "", err
			}
			msg = c.Message
		}
		parents := make([]string, len(c.Parents))
		for j, p := range c.Parents {
			if np, ok := remap[p]; ok {
				parents[j] = np
			} else {
				parents[j] = p
			}
		}
		newSHA, err := a.Git.CommitTree(ctx, c, parents, msg)
		if err != nil {
			return "", err
		}
		if newSHA == "" && a.DryRun {
			newSHA = "<rewritten-" + shortSHA(oldSHA) + ">"
		}
		remap[oldSHA] = newSHA
		newHead = newSHA
	}
	return newHead, nil
}

// ListCommitsWithNotes prints every commit reachable from rev (defaulting to
// HEAD) that has a git note, ordered from newest to oldest.
func (a *App) ListCommitsWithNotes(ctx context.Context, rev string) error {
	if rev == "" {
		rev = "HEAD"
	}
	noted, err := a.Git.NotesList(ctx)
	if err != nil {
		return err
	}
	if len(noted) == 0 {
		return nil
	}
	commits, err := a.Git.LogSubjects(ctx, rev)
	if err != nil {
		return err
	}
	for _, c := range commits {
		if noted[c.SHA] {
			a.printf("%s %s\n", c.SHA, c.Subject)
		}
	}
	return nil
}

// shortSHA abbreviates a hash for a message, leaving anything that is not one
// alone.
func shortSHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}
