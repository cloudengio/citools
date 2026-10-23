// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"strings"
)

// Git runs git commands in the current repository. The read-only methods take
// the reader and the ones that change something take the writer, which is how
// --dry-run is arranged: a dry run still has to read the repository to say
// what it would do.
type Git struct {
	read  Runner
	write Runner
}

// NewGit returns a Git that reads and writes with run.
func NewGit(run Runner) *Git {
	return &Git{read: run, write: run}
}

// DryRun returns a copy of g whose writes are announced rather than made.
func (g *Git) DryRun(announce func(string)) *Git {
	return &Git{read: g.read, write: dryRunner{announce: announce}}
}

func (g *Git) output(ctx context.Context, args ...string) (string, error) {
	out, err := g.read.Run(ctx, nil, "git", args...)
	return strings.TrimSpace(string(out)), err
}

// CurrentBranch returns the branch that is checked out.
func (g *Git) CurrentBranch(ctx context.Context) (string, error) {
	branch, err := g.output(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}
	return branch, nil
}

// MergeBase returns the commit at which ref diverged from the base branch. The
// remote-tracking branch is preferred, since it is what the pull request will
// be merged into, falling back to the local one when it is not there.
func (g *Git) MergeBase(ctx context.Context, ref, base string) (string, error) {
	if sha, err := g.output(ctx, "merge-base", ref, "origin/"+base); err == nil {
		return sha, nil
	}
	sha, err := g.output(ctx, "merge-base", ref, base)
	if err != nil {
		return "", fmt.Errorf("no merge base for %v and %v: %w", ref, base, err)
	}
	return sha, nil
}

// Diff returns the diff of the range, excluding the paths in exclude, which
// are git pathspecs.
func (g *Git) Diff(ctx context.Context, from, to string, exclude []string) (string, error) {
	args := append([]string{"diff", "-U3", from + ".." + to, "--", "."}, exclude...)
	out, err := g.read.Run(ctx, nil, "git", args...)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// DiffStat returns the summary of the range that Diff would return in full.
func (g *Git) DiffStat(ctx context.Context, from, to string) (string, error) {
	out, err := g.read.Run(ctx, nil, "git", "diff", "--stat", from+".."+to)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// RevParse resolves ref to a commit hash.
func (g *Git) RevParse(ctx context.Context, ref string) (string, error) {
	sha, err := g.output(ctx, "rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("cannot resolve %v: %w", ref, err)
	}
	return sha, nil
}

// TreeHash returns the hash of the tree that ref points at, ie. the content of
// the repository at that commit rather than the commit itself. Two refs with
// the same tree hash hold identical content however they came by it, which is
// what identifies a branch whose changes have already landed as a squash.
func (g *Git) TreeHash(ctx context.Context, ref string) (string, error) {
	sha, err := g.output(ctx, "rev-parse", ref+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("cannot resolve the tree of %v: %w", ref, err)
	}
	return sha, nil
}

// Subject returns the subject line of ref's commit message.
func (g *Git) Subject(ctx context.Context, ref string) (string, error) {
	return g.output(ctx, "log", "-1", "--pretty=%s", ref)
}

// RemoteBranchExists reports whether branch is on origin.
func (g *Git) RemoteBranchExists(ctx context.Context, branch string) (bool, error) {
	out, err := g.output(ctx, "ls-remote", "--heads", "origin", branch)
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// Fetch fetches refspecs from origin. A failure is returned rather than
// ignored: a stale remote-tracking branch makes every range that is derived
// from it wrong.
func (g *Git) Fetch(ctx context.Context, refspecs ...string) error {
	_, err := g.read.Run(ctx, nil, "git", append([]string{"fetch", "origin"}, refspecs...)...)
	return err
}

// PushBranch pushes branch to origin and sets it as the upstream.
func (g *Git) PushBranch(ctx context.Context, branch string) error {
	_, err := g.write.Run(ctx, nil, "git", "push", "--set-upstream", "origin", branch)
	return err
}

// AddNote attaches note to the commit, replacing any note already on it.
func (g *Git) AddNote(ctx context.Context, sha, note string) error {
	_, err := g.write.Run(ctx, []byte(note), "git", "notes", "add", "--force", "--file", "-", sha)
	return err
}

// Note returns the note attached to the commit, or the empty string if it has
// none.
func (g *Git) Note(ctx context.Context, sha string) string {
	note, err := g.output(ctx, "notes", "show", sha)
	if err != nil {
		return ""
	}
	return note
}

// PushNotes pushes the notes refs to origin.
func (g *Git) PushNotes(ctx context.Context) error {
	_, err := g.write.Run(ctx, nil, "git", "push", "origin", "refs/notes/*")
	return err
}

// DeleteBranch deletes the local branch. It uses git's own safety check,
// which refuses a branch that has not been merged, rather than forcing it:
// the caller has established that the content is already on the base branch,
// not that git agrees it was merged, and a branch is not worth losing to a
// disagreement.
func (g *Git) DeleteBranch(ctx context.Context, branch string) error {
	_, err := g.write.Run(ctx, nil, "git", "branch", "--delete", branch)
	return err
}

// DeleteRemoteBranch deletes branch from origin.
func (g *Git) DeleteRemoteBranch(ctx context.Context, branch string) error {
	_, err := g.write.Run(ctx, nil, "git", "push", "origin", "--delete", branch)
	return err
}
