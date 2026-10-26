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
// generated from what the branch changes.
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

	if err := a.ensurePushed(ctx, branch, push); err != nil {
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

// ensurePushed makes sure the branch is on origin, which GitHub requires
// before a pull request can refer to it.
func (a *App) ensurePushed(ctx context.Context, branch string, push bool) error {
	onOrigin, err := a.Git.RemoteBranchExists(ctx, branch)
	if err != nil {
		return err
	}
	if onOrigin {
		return nil
	}
	if !push {
		return fmt.Errorf("%v is not on origin, which github needs it to be: push it, or drop --push=false", branch)
	}
	ok, err := a.confirm(fmt.Sprintf("%v is not on origin yet, push it?", branch))
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
		what := "the local branch " + branch
		if deleteRemote {
			what = "the local and origin branch " + branch
		}
		actions = append(actions, "delete "+what+" if its content is what was merged")
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

// shortSHA abbreviates a hash for a message, leaving anything that is not one
// alone.
func shortSHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}
