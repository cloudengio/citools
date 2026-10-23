// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
)

func TestMarkdownToASCIIHeadings(t *testing.T) {
	for _, tc := range []struct{ md, want string }{
		{"# Overview", "=== Overview"},
		{"## Key Changes", "--- Key Changes"},
		{"### Details", "-- Details"},
		{"###### Deeper", "-- Deeper"},
		{"#NoSpace", "=== NoSpace"},
		// Not a heading: a hash that is not at the start of the line, and
		// one with nothing to head.
		{"a # b", "a # b"},
		{"#", "=== "},
	} {
		if got := MarkdownToASCII(tc.md); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.md, got, tc.want)
		}
	}
}

func TestMarkdownToASCIIInline(t *testing.T) {
	for _, tc := range []struct{ md, want string }{
		{"**bold**", "bold"},
		{"*italic*", "italic"},
		{"`code`", "code"},
		{"a **b** c *d* e `f` g", "a b c d e f g"},
		// Bold is stripped before italic, so the markers of one are not
		// mistaken for the other.
		{"**bold** and *italic*", "bold and italic"},
		{"[the docs](https://example.com/x)", "the docs (https://example.com/x)"},
		{"see [a](b) and [c](d)", "see a (b) and c (d)"},
		// Nothing to strip.
		{"plain text", "plain text"},
		{"2 * 3 * 4", "2 * 3 * 4"},
	} {
		if got := MarkdownToASCII(tc.md); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.md, got, tc.want)
		}
	}
}

func TestMarkdownToASCIIBullets(t *testing.T) {
	for _, tc := range []struct{ md, want string }{
		{"- one", "  * one"},
		{"* one", "  * one"},
		{"+ one", "  * one"},
		{"  - nested", "    * nested"},
		{"    - deeper", "      * deeper"},
		// A dash that is not a bullet marker.
		{"a - b", "a - b"},
		{"-no space", "-no space"},
	} {
		if got := MarkdownToASCII(tc.md); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.md, got, tc.want)
		}
	}
}

func TestMarkdownToASCIIFences(t *testing.T) {
	md := strings.Join([]string{
		"## Example",
		"```go",
		"// *not* italic, **not** bold",
		"x := `y`",
		"```",
		"after",
	}, "\n")
	want := strings.Join([]string{
		"--- Example",
		"// *not* italic, **not** bold",
		"x := `y`",
		"after",
	}, "\n")
	// The fences go, their content stays as written: it is code, and the
	// markers in it are not markup.
	if got := MarkdownToASCII(md); got != want {
		t.Errorf("got:\n%v\nwant:\n%v", got, want)
	}
}

func TestMarkdownToASCIINonASCII(t *testing.T) {
	for _, tc := range []struct{ md, want string }{
		{"a — b", "a -- b"},
		{"x → y", "x -> y"},
		{"“quoted”", `"quoted"`},
		{"don’t", "don't"},
		{"• bullet", "  * bullet"}, // becomes an ascii bullet, then a list marker
		{"✓ done", "[x] done"},
		{"a b", "a b"},
		{"1 ≤ 2", "1 <= 2"},
		{"wait…", "wait..."},
		// Anything else outside ascii is dropped rather than guessed at.
		{"emoji \U0001f600 here", "emoji  here"},
		{"中文", ""},
	} {
		got := MarkdownToASCII(tc.md)
		if got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.md, got, tc.want)
		}
		if !isASCII(got) {
			t.Errorf("%q: got %q, which is not ascii", tc.md, got)
		}
	}
}

func TestMarkdownToASCIIDocument(t *testing.T) {
	md := strings.Join([]string{
		"## Overview",
		"",
		"Replaces the **shell** functions with `ghpr` — see [the readme](README.md).",
		"",
		"## Key Changes",
		"",
		"- `ghpr create-pr` raises the PR",
		"  - uses *claude* or *agy*",
		"",
		"## Breaking Changes",
		"",
		"None",
	}, "\n")
	want := strings.Join([]string{
		"--- Overview",
		"",
		"Replaces the shell functions with ghpr -- see the readme (README.md).",
		"",
		"--- Key Changes",
		"",
		"  * ghpr create-pr raises the PR",
		"    * uses claude or agy",
		"",
		"--- Breaking Changes",
		"",
		"None",
	}, "\n")
	if got := MarkdownToASCII(md); got != want {
		t.Errorf("got:\n%v\nwant:\n%v", got, want)
	}
}

func TestMarkdownToASCIIPreservesStructure(t *testing.T) {
	// Blank lines and line order are the only structure a note has left, so
	// they survive.
	md := "a\n\n\nb\n"
	if got, want := MarkdownToASCII(md), "a\n\n\nb\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := MarkdownToASCII("a\r\nb"), "a\nb"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := MarkdownToASCII(""), ""; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
