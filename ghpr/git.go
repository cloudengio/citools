// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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

// MergeFFOnly fast-forwards the current branch to ref, failing if a merge commit
// would be needed.
func (g *Git) MergeFFOnly(ctx context.Context, ref string) error {
	_, err := g.write.Run(ctx, nil, "git", "merge", "--ff-only", ref)
	return err
}

// FetchUpdateBranch fetches branch from origin and fast-forwards the local
// branch of the same name without checking it out.
func (g *Git) FetchUpdateBranch(ctx context.Context, branch string) error {
	_, err := g.write.Run(ctx, nil, "git", "fetch", "origin", branch+":"+branch)
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

// NotesList returns the set of commit SHAs that have a git note attached.
func (g *Git) NotesList(ctx context.Context) (map[string]bool, error) {
	out, err := g.output(ctx, "notes", "list")
	if err != nil {
		return nil, err
	}
	noted := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			noted[fields[1]] = true
		}
	}
	return noted, nil
}

// CommitSummary holds a commit SHA and its subject line.
type CommitSummary struct {
	SHA     string
	Subject string
}

// LogSubjects returns commit SHAs and subjects reachable from rev, ordered from
// newest to oldest.
func (g *Git) LogSubjects(ctx context.Context, rev string) ([]CommitSummary, error) {
	out, err := g.output(ctx, "log", "--format=%H%x09%s", rev)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var commits []CommitSummary
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		sha, subject, _ := strings.Cut(line, "\t")
		commits = append(commits, CommitSummary{SHA: sha, Subject: subject})
	}
	return commits, nil
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

// CommitIdentity holds the name, email, and raw timestamp of a commit author
// or committer.
type CommitIdentity struct {
	Name  string
	Email string
	Date  string
}

// Commit holds the parsed structure of a git commit object as returned by
// git cat-file commit.
type Commit struct {
	Tree      string
	Parents   []string
	Author    CommitIdentity
	Committer CommitIdentity
	Message   string
}

// Subject returns the first line of the commit message.
func (c *Commit) Subject() string {
	first, _, _ := strings.Cut(c.Message, "\n")
	return strings.TrimSpace(first)
}

// WorkingTreeDirty reports whether the working tree or index has uncommitted
// changes.
func (g *Git) WorkingTreeDirty(ctx context.Context) (bool, error) {
	out, err := g.output(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// StatusShort returns the short working tree status output, including all
// untracked files regardless of git status.showUntrackedFiles configuration.
func (g *Git) StatusShort(ctx context.Context) (string, error) {
	out, err := g.read.Run(ctx, nil, "git", "status", "--short", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// StageAll stages all modified, deleted, and untracked files in the working
// tree.
func (g *Git) StageAll(ctx context.Context) error {
	_, err := g.write.Run(ctx, nil, "git", "add", "-A")
	return err
}

// Commit creates a new commit with message read from standard input.
func (g *Git) Commit(ctx context.Context, message string) error {
	_, err := g.write.Run(ctx, []byte(message), "git", "commit", "-F", "-")
	return err
}

// IsAncestor reports whether ancestor (which must be a full commit SHA) is an
// ancestor of descendant.
func (g *Git) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	out, err := g.output(ctx, "merge-base", ancestor, descendant)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return out == ancestor, nil
}

// RevList returns commit SHAs on the direct ancestry path in revRange, ordered
// from oldest to newest.
func (g *Git) RevList(ctx context.Context, revRange string) ([]string, error) {
	out, err := g.output(ctx, "rev-list", "--ancestry-path", "--reverse", "--topo-order", revRange)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// ReadCommit reads and parses the git commit object at rev.
func (g *Git) ReadCommit(ctx context.Context, rev string) (*Commit, error) {
	out, err := g.read.Run(ctx, nil, "git", "cat-file", "commit", rev)
	if err != nil {
		return nil, err
	}
	return parseCommit(string(out))
}

// CommitTree writes a new commit object with c's tree, authorship, and
// timestamps, the given parents and commit message, and returns its SHA.
func (g *Git) CommitTree(ctx context.Context, c *Commit, parents []string, message string) (string, error) {
	args := []string{"commit-tree", c.Tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	args = append(args, "-F", "-")
	env := []string{
		"GIT_AUTHOR_NAME=" + c.Author.Name,
		"GIT_AUTHOR_EMAIL=" + c.Author.Email,
		"GIT_AUTHOR_DATE=" + c.Author.Date,
		"GIT_COMMITTER_NAME=" + c.Committer.Name,
		"GIT_COMMITTER_EMAIL=" + c.Committer.Email,
		"GIT_COMMITTER_DATE=" + c.Committer.Date,
	}
	out, err := runWithEnv(ctx, g.write, env, []byte(message), "git", args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ResetKeep moves the current branch and index to sha while preserving any
// unrelated checks via git reset --keep.
func (g *Git) ResetKeep(ctx context.Context, sha string) error {
	_, err := g.write.Run(ctx, nil, "git", "reset", "--keep", sha)
	return err
}

// PushForceWithLease force-pushes branch to origin with --force-with-lease.
func (g *Git) PushForceWithLease(ctx context.Context, branch string) error {
	_, err := g.write.Run(ctx, nil, "git", "push", "--force-with-lease", "origin", branch)
	return err
}

func parseCommit(raw string) (*Commit, error) {
	header, message, found := strings.Cut(raw, "\n\n")
	if !found {
		return nil, fmt.Errorf("malformed commit object: missing header separator")
	}
	c := &Commit{Message: message}
	for line := range strings.SplitSeq(header, "\n") {
		// Multi-line headers such as gpgsig continue with a leading space.
		if strings.HasPrefix(line, " ") {
			continue
		}
		key, val, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch key {
		case "tree":
			c.Tree = val
		case "parent":
			c.Parents = append(c.Parents, val)
		case "author":
			id, err := parseIdentity(val)
			if err != nil {
				return nil, fmt.Errorf("commit author: %w", err)
			}
			c.Author = id
		case "committer":
			id, err := parseIdentity(val)
			if err != nil {
				return nil, fmt.Errorf("commit committer: %w", err)
			}
			c.Committer = id
		}
	}
	if c.Tree == "" {
		return nil, fmt.Errorf("malformed commit object: missing tree")
	}
	return c, nil
}

func parseIdentity(s string) (CommitIdentity, error) {
	lt := strings.LastIndexByte(s, '<')
	if lt < 0 {
		return CommitIdentity{}, fmt.Errorf("missing email in %q", s)
	}
	gtOff := strings.IndexByte(s[lt:], '>')
	if gtOff < 0 {
		return CommitIdentity{}, fmt.Errorf("unterminated email in %q", s)
	}
	gt := lt + gtOff
	return CommitIdentity{
		Name:  strings.TrimSpace(s[:lt]),
		Email: s[lt+1 : gt],
		Date:  strings.TrimSpace(s[gt+1:]),
	}, nil
}
