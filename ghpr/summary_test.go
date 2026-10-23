// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// newTestSummarizer returns a Summarizer that runs everything through run.
func newTestSummarizer(run Runner, engine, model string) *Summarizer {
	return &Summarizer{
		git:          NewGit(run),
		run:          run,
		engine:       engine,
		model:        model,
		maxDiffLines: 3000,
		log:          func(string, ...any) {},
	}
}

// scriptMergeBaseAndDiff scripts the two commands every summary starts with.
func scriptMergeBaseAndDiff(run *fakeRunner, diff string) *fakeRunner {
	return run.
		expect("git merge-base my-feature origin/main", "mergebase\n", nil).
		expect(diffCmd("mergebase", "my-feature"), diff, nil)
}

func TestSummarizeWithClaude(t *testing.T) {
	run := newFakeRunner(t)
	scriptMergeBaseAndDiff(run, "diff --git a/x b/x\n+added\n").
		expect(`claude -p --allowedTools ""`, "## Overview\nit adds x\n", nil)

	summary := must(newTestSummarizer(run, "claude", "").Summarize(t.Context(), "main", "my-feature"))
	if got, want := summary, "## Overview\nit adds x"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// The instructions and the diff are given on standard input: a diff does
	// not fit on a command line, and the agent is denied tools so it cannot
	// go and read one.
	stdin := run.stdinFor("claude")
	for _, want := range []string{"senior software engineer", "## Key Changes", "+added"} {
		if !strings.Contains(stdin, want) {
			t.Errorf("the prompt does not contain %q", want)
		}
	}
	run.assertNoUnexpected()
}

func TestSummarizeWithClaudeModel(t *testing.T) {
	run := newFakeRunner(t)
	scriptMergeBaseAndDiff(run, "a diff\n").
		expect(`claude -p --allowedTools "" --model claude-opus-5`, "a summary", nil)
	if _, err := newTestSummarizer(run, "claude", "claude-opus-5").Summarize(t.Context(), "main", "my-feature"); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	run.assertNoUnexpected()
}

func TestSummarizeWithAgy(t *testing.T) {
	run := newFakeRunner(t)
	scriptMergeBaseAndDiff(run, "a diff\n").
		expectPrefix("agy -p ", "a summary", nil)
	if _, err := newTestSummarizer(run, "agy", "gemini-3-pro").Summarize(t.Context(), "main", "my-feature"); err != nil {
		t.Fatalf("Summarize: %v", err)
	}

	var cmd string
	for _, c := range run.ranCommands() {
		if strings.HasPrefix(c, "agy") {
			cmd = c
		}
	}
	// agy reads the diff from a file, so the prompt has to name one, and the
	// file has to have held the diff.
	if !strings.Contains(cmd, "--model gemini-3-pro") {
		t.Errorf("%v does not pass the model", cmd)
	}
	path := diffPathFromPrompt(t, cmd)
	if !strings.Contains(path, "ghpr-diff-") {
		t.Errorf("the prompt names %q, which is not a diff file", path)
	}
	// And it must be cleaned up: the diff of a private repository is not
	// left in the temporary directory.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%v was left behind: %v", path, err)
	}
	run.assertNoUnexpected()
}

// diffPathFromPrompt extracts the file name agy was pointed at.
func diffPathFromPrompt(t *testing.T, cmd string) string {
	t.Helper()
	const marker = "summarize is in the file: "
	i := strings.Index(cmd, marker)
	if i < 0 {
		t.Fatalf("%v does not name a diff file", cmd)
	}
	rest := cmd[i+len(marker):]
	if end := strings.Index(rest, `"`); end >= 0 {
		return strings.TrimSpace(rest[:end])
	}
	return strings.TrimSpace(rest)
}

func TestSummarizeWithCustomPrompt(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		run := newFakeRunner(t)
		scriptMergeBaseAndDiff(run, "diff --git a/x b/x\n+added\n").
			expect(`claude -p --allowedTools ""`, "summary\n", nil)

		s := newTestSummarizer(run, "claude", "")
		s.prompt = "My custom PR prompt."
		summary := must(s.Summarize(t.Context(), "main", "my-feature"))
		if got, want := summary, "summary"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}

		stdin := run.stdinFor("claude")
		if !strings.Contains(stdin, "My custom PR prompt.") {
			t.Errorf("prompt was not in stdin: %q", stdin)
		}
		if strings.Contains(stdin, "senior software engineer") {
			t.Errorf("default instructions unexpectedly present: %q", stdin)
		}
		run.assertNoUnexpected()
	})

	t.Run("agy", func(t *testing.T) {
		run := newFakeRunner(t)
		scriptMergeBaseAndDiff(run, "a diff\n").
			expectPrefix("agy -p ", "summary", nil)

		s := newTestSummarizer(run, "agy", "")
		s.prompt = "My custom PR prompt."
		if _, err := s.Summarize(t.Context(), "main", "my-feature"); err != nil {
			t.Fatalf("Summarize: %v", err)
		}

		var cmd string
		for _, c := range run.ranCommands() {
			if strings.HasPrefix(c, "agy") {
				cmd = c
			}
		}
		if !strings.Contains(cmd, "My custom PR prompt.") {
			t.Errorf("prompt was not in command: %q", cmd)
		}
		if strings.Contains(cmd, "senior software engineer") {
			t.Errorf("default instructions unexpectedly present: %q", cmd)
		}
		run.assertNoUnexpected()
	})

	t.Run("tool-specific prompts override default prompt", func(t *testing.T) {
		run := newFakeRunner(t)
		scriptMergeBaseAndDiff(run, "diff --git a/x b/x\n+added\n").
			expect(`claude -p --allowedTools ""`, "summary\n", nil)

		s := newTestSummarizer(run, "claude", "")
		s.prompt = "Default fallback prompt."
		s.prompts = map[string]string{
			"claude": "Claude specific prompt.",
			"agy":    "Agy specific prompt.",
		}
		summary := must(s.Summarize(t.Context(), "main", "my-feature"))
		if got, want := summary, "summary"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}

		stdin := run.stdinFor("claude")
		if !strings.Contains(stdin, "Claude specific prompt.") {
			t.Errorf("expected claude specific prompt: %q", stdin)
		}
		if strings.Contains(stdin, "Default fallback prompt.") {
			t.Errorf("default fallback prompt was unexpectedly used: %q", stdin)
		}
		run.assertNoUnexpected()
	})
}

func TestSummarizeUnknownEngine(t *testing.T) {
	run := newFakeRunner(t)
	scriptMergeBaseAndDiff(run, "a diff\n")
	_, err := newTestSummarizer(run, "gpt", "").Summarize(t.Context(), "main", "my-feature")
	if err == nil || !strings.Contains(err.Error(), "unsupported engine") {
		t.Errorf("got %v, want an unsupported engine error", err)
	}
}

func TestSummarizeEmptyDiff(t *testing.T) {
	// Nothing to describe is a failure with a reason, rather than an agent
	// asked to summarize nothing.
	for _, diff := range []string{"", "   \n\n"} {
		run := newFakeRunner(t)
		scriptMergeBaseAndDiff(run, diff)
		_, err := newTestSummarizer(run, "claude", "").Summarize(t.Context(), "main", "my-feature")
		if err == nil || !strings.Contains(err.Error(), "makes no changes to main") {
			t.Errorf("%q: got %v, want an empty diff error", diff, err)
		}
		if run.ranPrefix("claude") {
			t.Error("the agent was run for an empty diff")
		}
	}
}

func TestSummarizeLargeDiffUsesStat(t *testing.T) {
	// A description derived from which files changed is more use than one
	// derived from however much of a huge diff fits.
	big := strings.Repeat("+a line\n", 50)
	run := newFakeRunner(t).
		expect("git merge-base my-feature origin/main", "mergebase\n", nil).
		expect(diffCmd("mergebase", "my-feature"), big, nil).
		expect("git diff --stat mergebase..my-feature", " x | 2 +-\n", nil).
		expect(`claude -p --allowedTools ""`, "a summary", nil)

	s := newTestSummarizer(run, "claude", "")
	s.maxDiffLines = 10
	if _, err := s.Summarize(t.Context(), "main", "my-feature"); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if stdin := run.stdinFor("claude"); !strings.Contains(stdin, "x | 2 +-") {
		t.Errorf("the prompt does not contain the stat: %q", stdin)
	} else if strings.Contains(stdin, "+a line") {
		t.Error("the prompt contains the diff as well as the stat")
	}
	run.assertNoUnexpected()
}

func TestSummarizeNoLimit(t *testing.T) {
	big := strings.Repeat("+a line\n", 50)
	run := newFakeRunner(t)
	scriptMergeBaseAndDiff(run, big).
		expect(`claude -p --allowedTools ""`, "a summary", nil)
	s := newTestSummarizer(run, "claude", "")
	s.maxDiffLines = 0 // no limit
	if _, err := s.Summarize(t.Context(), "main", "my-feature"); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if stdin := run.stdinFor("claude"); !strings.Contains(stdin, "+a line") {
		t.Error("the whole diff should be summarized when there is no limit")
	}
	run.assertNoUnexpected()
}

func TestSummarizeAgentFails(t *testing.T) {
	run := newFakeRunner(t)
	scriptMergeBaseAndDiff(run, "a diff\n").
		expect(`claude -p --allowedTools ""`, "", errors.New("claude: not logged in"))
	_, err := newTestSummarizer(run, "claude", "").Summarize(t.Context(), "main", "my-feature")
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("got %v, want the agent's error", err)
	}
}

func TestSummarizeEmptyAnswer(t *testing.T) {
	// An agent that answers with nothing must not become a pull request with
	// no description.
	for _, answer := range []string{"", "\n\n  \n"} {
		run := newFakeRunner(t)
		scriptMergeBaseAndDiff(run, "a diff\n").
			expect(`claude -p --allowedTools ""`, answer, nil)
		_, err := newTestSummarizer(run, "claude", "").Summarize(t.Context(), "main", "my-feature")
		if err == nil || !strings.Contains(err.Error(), "empty summary") {
			t.Errorf("%q: got %v, want an empty summary error", answer, err)
		}
	}
}

func TestCleanSummary(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"trimmed", "\n  a summary  \n\n", "a summary"},
		{
			// Models wrap their answer in a fence however firmly they are
			// asked not to, and the fence is not part of the description.
			"unwrapped", "```markdown\n## Overview\ntext\n```", "## Overview\ntext",
		},
		{"unwrapped without a language", "```\ntext\n```", "text"},
		{
			// A fence that is part of the description stays: the outer lines
			// are then the edges of a code block, not a wrapper.
			"inner fence kept",
			"## Overview\n\n```go\nx := 1\n```\n\ndone",
			"## Overview\n\n```go\nx := 1\n```\n\ndone",
		},
		{
			"wrapper with an inner fence is left alone",
			"```\n## Overview\n```go\nx := 1\n```\n```",
			"```\n## Overview\n```go\nx := 1\n```\n```",
		},
		{"no fence", "## Overview\ntext", "## Overview\ntext"},
		{"one line", "```", "```"},
		{"empty", "", ""},
	} {
		if got := cleanSummary(tc.in); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestEngineDescription(t *testing.T) {
	if got, want := newTestSummarizer(nil, "claude", "").engineDescription(), "claude"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := newTestSummarizer(nil, "agy", "opus").engineDescription(), "agy (opus)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSummarizerCommandClaude(t *testing.T) {
	run := newFakeRunner(t)
	scriptMergeBaseAndDiff(run, "diff --git a/x b/x\n+added\n")

	s := newTestSummarizer(run, "claude", "claude-sonnet-4")
	cmd, err := s.Command(t.Context(), "main", "my-feature")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if !strings.HasPrefix(cmd, `claude -p --allowedTools "" --model claude-sonnet-4 << 'EOF'`) {
		t.Errorf("unexpected command line prefix: %v", cmd)
	}
	if !strings.Contains(cmd, "+added") {
		t.Errorf("command does not contain diff content: %v", cmd)
	}
	if !strings.Contains(cmd, "senior software engineer") {
		t.Errorf("command does not contain instructions: %v", cmd)
	}
	if !strings.HasSuffix(cmd, "\nEOF") {
		t.Errorf("command does not end with EOF: %v", cmd)
	}
	run.assertNoUnexpected()
}

func TestSummarizerCommandAgy(t *testing.T) {
	run := newFakeRunner(t)
	scriptMergeBaseAndDiff(run, "diff --git a/x b/x\n+added\n")

	s := newTestSummarizer(run, "agy", "gemini-3-pro")
	cmd, err := s.Command(t.Context(), "main", "my-feature")
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if !strings.HasPrefix(cmd, "agy -p ") {
		t.Errorf("expected agy command to start with agy -p: %v", cmd)
	}
	if !strings.Contains(cmd, "--model gemini-3-pro") {
		t.Errorf("expected agy command to include model: %v", cmd)
	}
	path := diffPathFromPrompt(t, cmd)
	t.Cleanup(func() { _ = os.Remove(path) })
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading temp diff: %v", err)
	}
	if !strings.Contains(string(content), "+added") {
		t.Errorf("temp diff file did not contain diff content")
	}
	run.assertNoUnexpected()
}
