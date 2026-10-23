// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// excludedPaths are left out of the diff an agent is asked to summarize.
// They are generated or vendored content: they say nothing about the intent
// of a change and crowd out what does.
var excludedPaths = []string{
	":!package-lock.json",
	":!pnpm-lock.yaml",
	":!yarn.lock",
	":!go.sum",
	":!*.pb.go",
	":!*.gen.*",
	":!*.min.js",
}

// defaultInstructions is what the agent is asked to do by default when no
// custom prompt is configured. It asks for the shape of description this
// workflow wants and says not to use tools, since the diff is supplied and
// reading the repository would only invite a summary of something other
// than the change.
const defaultInstructions = `You are a senior software engineer. Generate a tight, minimal GitHub Pull Request description based ONLY on the provided git diff.

Format:
## Overview
(1-2 concise sentences summarizing the high-level intent and scope)

## Key Changes
(Strictly concise bullet points grouped by component or package; no filler text)

## Breaking Changes
(State 'None' if applicable)

Output raw Markdown only. Do not wrap in outer code blocks. Do not attempt to run tools.`

// Summarizer generates a pull request description from the diff of a branch.
type Summarizer struct {
	git          *Git
	run          Runner
	engine       string
	model        string
	maxDiffLines int
	prompt       string
	prompts      map[string]string
	log          func(string, ...any)
}

func (s *Summarizer) instructions() string {
	if s.prompts != nil {
		if p, ok := s.prompts[s.engine]; ok && strings.TrimSpace(p) != "" {
			return p
		}
	}
	if strings.TrimSpace(s.prompt) != "" {
		return s.prompt
	}
	return defaultInstructions
}

// Summarize returns a description of what ref changes relative to the base
// branch. The diff is taken from the merge base so that it describes what the
// branch did rather than what has happened on the base branch since.
func (s *Summarizer) Summarize(ctx context.Context, base, ref string) (string, error) {
	diff, err := s.diff(ctx, base, ref)
	if err != nil {
		return "", err
	}
	s.log("summarizing %v lines of diff with %v", strings.Count(diff, "\n"), s.engineDescription())
	summary, err := s.generate(ctx, diff)
	if err != nil {
		return "", err
	}
	summary = cleanSummary(summary)
	if summary == "" {
		return "", fmt.Errorf("%v produced an empty summary", s.engineDescription())
	}
	return summary, nil
}

func (s *Summarizer) engineDescription() string {
	if s.model == "" {
		return s.engine
	}
	return s.engine + " (" + s.model + ")"
}

// diff returns the diff to be summarized, which is the stat summary instead
// when the diff is too large: a description derived from which files changed
// is more use than one derived from the first part of a diff.
func (s *Summarizer) diff(ctx context.Context, base, ref string) (string, error) {
	mergeBase, err := s.git.MergeBase(ctx, ref, base)
	if err != nil {
		return "", err
	}
	diff, err := s.git.Diff(ctx, mergeBase, ref, excludedPaths)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(diff) == "" {
		return "", fmt.Errorf("%v makes no changes to %v", ref, base)
	}
	if lines := strings.Count(diff, "\n"); s.maxDiffLines > 0 && lines > s.maxDiffLines {
		s.log("the diff is %v lines, over the %v line limit: summarizing the stat instead", lines, s.maxDiffLines)
		stat, err := s.git.DiffStat(ctx, mergeBase, ref)
		if err != nil {
			return "", err
		}
		return stat, nil
	}
	return diff, nil
}

func (s *Summarizer) command(diff string) (stdin []byte, name string, args []string, cleanup func(), err error) {
	switch s.engine {
	case "claude":
		args := []string{"-p", "--allowedTools", ""}
		if s.model != "" {
			args = append(args, "--model", s.model)
		}
		prompt := s.instructions() + "\n\n### Diff:\n" + diff
		return []byte(prompt), "claude", args, func() {}, nil
	case "agy":
		path, cleanup, err := writeTempDiff(diff)
		if err != nil {
			return nil, "", nil, nil, err
		}
		args := []string{"-p", s.instructions() + "\n\nThe diff to summarize is in the file: " + path}
		if s.model != "" {
			args = append(args, "--model", s.model)
		}
		return nil, "agy", args, cleanup, nil
	}
	return nil, "", nil, nil, fmt.Errorf("unsupported engine %q: use claude or agy", s.engine)
}

// Command returns the command line that would be run to generate the summary
// for ref relative to base.
func (s *Summarizer) Command(ctx context.Context, base, ref string) (string, error) {
	diff, err := s.diff(ctx, base, ref)
	if err != nil {
		return "", err
	}
	stdin, name, args, _, err := s.command(diff)
	if err != nil {
		return "", err
	}
	return formatCommand(stdin, name, args...), nil
}

// generate runs the agent. claude is given the diff on its standard input,
// with tools denied, so that it summarizes what it was handed and nothing
// else. agy is given the path of a file holding the diff, which is how it
// takes input that does not fit on a command line.
func (s *Summarizer) generate(ctx context.Context, diff string) (string, error) {
	stdin, name, args, cleanup, err := s.command(diff)
	if err != nil {
		return "", err
	}
	defer cleanup()
	out, err := s.run.Run(ctx, stdin, name, args...)
	return string(out), err
}

// writeTempDiff writes the diff to a file for an agent that reads its input
// from one, and returns its path and a function that removes it.
func writeTempDiff(diff string) (string, func(), error) {
	f, err := os.CreateTemp("", "ghpr-diff-*.patch")
	if err != nil {
		return "", nil, fmt.Errorf("cannot create a file for the diff: %w", err)
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	if _, err := f.WriteString(diff); err != nil {
		_ = f.Close() // The write already failed; this is only tidying up.
		cleanup()
		return "", nil, fmt.Errorf("cannot write the diff to %v: %w", filepath.Base(f.Name()), err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("cannot write the diff to %v: %w", filepath.Base(f.Name()), err)
	}
	return f.Name(), cleanup, nil
}

// cleanSummary trims the agent's output and unwraps it from an outer code
// fence, which a model adds from time to time however firmly it is asked not
// to. A fence that is the whole of the output is markup around the
// description; one that is part of it belongs to the description.
func cleanSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	lines := strings.Split(summary, "\n")
	if len(lines) < 2 {
		return summary
	}
	first, last := strings.TrimSpace(lines[0]), strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(first, "```") || last != "```" {
		return summary
	}
	// Only when nothing between them opens a fence of its own, which would
	// make these two the outside of a code block rather than a wrapper.
	for _, line := range lines[1 : len(lines)-1] {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			return summary
		}
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}
