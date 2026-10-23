// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"regexp"
	"strings"
)

var (
	atxHeading  = regexp.MustCompile(`^(#{1,6})[[:space:]]*(.*)$`)
	boldSpan    = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	italicSpan  = regexp.MustCompile(`\*([^\s*](?:[^*]*[^\s*])?)\*`)
	codeSpan    = regexp.MustCompile("`([^`]+)`")
	bulletStart = regexp.MustCompile(`^([[:space:]]*)[*+-][[:space:]]+`)
	linkSpan    = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

// transliterations are the non-ascii characters an agent writes often enough
// to be worth keeping the sense of. Anything else outside ascii is dropped,
// since a note is read with git log in a terminal of unknown capability.
var transliterations = strings.NewReplacer(
	"—", "--", // em dash
	"–", "-", // en dash
	"‘", "'", // left single quote
	"’", "'", // right single quote
	"“", `"`, // left double quote
	"”", `"`, // right double quote
	"…", "...", // ellipsis
	"→", "->", // rightwards arrow
	"←", "<-", // leftwards arrow
	"↔", "<->", // left right arrow
	"⇒", "=>", // rightwards double arrow
	"•", "*", // bullet
	"·", "*", // middle dot
	"✓", "[x]", // check mark
	"✔", "[x]", // heavy check mark
	"✗", "[ ]", // ballot x
	"✘", "[ ]", // heavy ballot x
	"≤", "<=", // less than or equal
	"≥", ">=", // greater than or equal
	"×", "x", // multiplication sign
	"≈", "~", // almost equal
	" ", " ", // non-breaking space
	"°", " deg", // degree sign
	"«", "<<", // left guillemet
	"»", ">>", // right guillemet
)

// MarkdownToASCII renders the markdown of a pull request description as the
// plain text of a git note. Notes are read with git log in a terminal, where
// markdown is noise rather than formatting, so the markup is reduced to
// something that reads as text:
//
//   - a level one heading becomes "=== text", a level two "--- text" and a
//     deeper one "-- text";
//   - bold, italic and code spans lose their markers;
//   - a link becomes "text (url)";
//   - a bullet becomes "  * text", keeping its indentation;
//   - a fenced code block loses its fences, keeping its content;
//   - the non-ascii characters an agent tends to use are transliterated and
//     anything else outside ascii is dropped. This happens first, so that a
//     bullet written as a bullet character is recognised as a list marker.
//
// The content of a fenced code block is left alone apart from that: it is
// code, and the markers in it are not markup.
func MarkdownToASCII(md string) string {
	var out []string
	inFence := false
	for line := range strings.SplitSeq(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if fence := strings.TrimSpace(line); strings.HasPrefix(fence, "```") || strings.HasPrefix(fence, "~~~") {
			inFence = !inFence
			continue
		}
		line = toASCII(line)
		if inFence {
			out = append(out, line)
			continue
		}
		out = append(out, inlineToText(headingToText(line)))
	}
	return strings.Join(out, "\n")
}

// headingToText replaces an atx heading with a text banner, and a bullet
// marker with a consistent one, neither of which means anything to a reader
// of plain text as written.
func headingToText(line string) string {
	if m := atxHeading.FindStringSubmatch(line); m != nil {
		switch len(m[1]) {
		case 1:
			return "=== " + m[2]
		case 2:
			return "--- " + m[2]
		default:
			return "-- " + m[2]
		}
	}
	if m := bulletStart.FindStringSubmatch(line); m != nil {
		return m[1] + "  * " + line[len(m[0]):]
	}
	return line
}

// inlineToText strips the inline markup, leaving what it was marking up.
// Bold is replaced before italic, since the markers of the one contain the
// markers of the other.
func inlineToText(line string) string {
	line = linkSpan.ReplaceAllString(line, "$1 ($2)")
	line = boldSpan.ReplaceAllString(line, "$1")
	line = italicSpan.ReplaceAllString(line, "$1")
	return codeSpan.ReplaceAllString(line, "$1")
}

// toASCII transliterates what it can and drops the rest, so that the result
// is ascii whatever the agent wrote. A tab is kept: it is ascii, and it is
// indentation in a code block.
func toASCII(s string) string {
	s = transliterations.Replace(s)
	if isASCII(s) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if r < 0x80 {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
