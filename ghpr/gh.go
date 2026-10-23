// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"
)

// prFields are the fields of a pull request that ghpr asks gh for. They are
// requested together because gh makes one api call per view.
const prFields = "number,state,body,title,url,headRefName,headRefOid,mergeCommit"

// PR is the part of a GitHub pull request that ghpr uses.
type PR struct {
	Number      int    `json:"number"`
	State       string `json:"state"` // OPEN, CLOSED or MERGED
	Body        string `json:"body"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
	MergeCommit *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
}

// Merged reports whether the pull request has been merged.
func (p *PR) Merged() bool { return p.State == "MERGED" }

// MergeSHA returns the commit the pull request created on the base branch,
// which for a squash merge is the single commit holding all of its changes.
func (p *PR) MergeSHA() string {
	if p.MergeCommit == nil {
		return ""
	}
	return p.MergeCommit.Oid
}

// ErrNoPR reports that a branch has no pull request.
var ErrNoPR = errors.New("no pull request found")

// transientMessages are what gh reports when github could not be reached, or
// asked to be tried again, rather than answering about the pull request. They
// are matched in the text because gh exits 1 for every failure alike, so its
// message is all there is to go on.
var transientMessages = []string{
	"TLS handshake timeout",
	"i/o timeout",
	"net/http: timeout",
	"connection reset by peer",
	"connection refused",
	"no such host",
	"temporary failure in name resolution",
	"502 Bad Gateway",
	"503 Service Unavailable",
	"504 Gateway Timeout",
	"was submitted too quickly",
}

// isTransient reports whether err is a failure to reach github rather than an
// answer from it. Such a failure says nothing about the pull request either
// way, so a read that hit one is worth repeating.
func isTransient(err error) bool {
	if err == nil || errors.Is(err, ErrNoPR) {
		return false
	}
	msg := err.Error()
	for _, t := range transientMessages {
		if strings.Contains(msg, t) {
			return true
		}
	}
	return false
}

// GH runs the GitHub cli. As with Git, reads and writes are separated so that
// a dry run can still look things up.
type GH struct {
	read  Runner
	write Runner

	// attempts and retryDelay bound how long a read is retried for when
	// github cannot be reached. A write is never retried: gh reports a
	// failure the same way whether the request arrived or not, and
	// repeating a mutation could raise a second pull request.
	attempts   int
	retryDelay time.Duration

	// notify, when set, is told of a failure that is about to be retried,
	// since a command that pauses for seconds should say why.
	notify func(string, ...any)
}

// NewGH returns a GH that reads and writes with run.
func NewGH(run Runner) *GH {
	return &GH{read: run, write: run, attempts: 3, retryDelay: time.Second}
}

// DryRun returns a copy of h whose writes are announced rather than made.
func (h *GH) DryRun(announce func(string)) *GH {
	cp := *h
	cp.write = dryRunner{announce: announce}
	return &cp
}

// View returns the pull request for target, which may be a branch name, a
// pull request number or a url. It returns ErrNoPR if there is none, which is
// not a failure: it is how create-pr establishes that there is one to create.
func (h *GH) View(ctx context.Context, target string) (*PR, error) {
	attempts := max(h.attempts, 1)
	for attempt := 1; ; attempt++ {
		pr, err := h.view(ctx, target)
		if err == nil || !isTransient(err) {
			return pr, err
		}
		if attempt >= attempts {
			return nil, fmt.Errorf("github could not be reached in %v attempts: %w", attempts, err)
		}
		if h.notify != nil {
			h.notify("github could not be reached (attempt %v of %v), retrying in %v: %v\n",
				attempt, attempts, h.retryDelay, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(h.retryDelay):
		}
	}
}

func (h *GH) view(ctx context.Context, target string) (*PR, error) {
	out, err := h.read.Run(ctx, nil, "gh", "pr", "view", target, "--json", prFields)
	if err != nil {
		// gh says this on stderr and exits 1, which is indistinguishable
		// from a real failure except by the message.
		if strings.Contains(err.Error(), "no pull requests found") ||
			strings.Contains(err.Error(), "Could not resolve to a PullRequest") {
			return nil, fmt.Errorf("%w for %v", ErrNoPR, target)
		}
		return nil, err
	}
	var pr PR
	if err := json.Unmarshal(out, &pr); err != nil {
		return nil, fmt.Errorf("cannot parse the pull request for %v: %w", target, err)
	}
	return &pr, nil
}

// Create creates a pull request for branch and returns its url. The body is
// passed on standard input rather than as an argument, since a generated
// summary can be longer than a command line may be.
func (h *GH) Create(ctx context.Context, base, branch, title, body string, draft bool) (string, error) {
	args := []string{"pr", "create", "--base", base, "--head", branch,
		"--title", title, "--body-file", "-"}
	if draft {
		args = append(args, "--draft")
	}
	out, err := h.write.Run(ctx, []byte(body), "gh", args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// UpdateBody replaces the description of the pull request.
func (h *GH) UpdateBody(ctx context.Context, number int, body string) error {
	_, err := h.write.Run(ctx, []byte(body), "gh", "pr", "edit",
		fmt.Sprintf("%d", number), "--body-file", "-")
	return err
}
