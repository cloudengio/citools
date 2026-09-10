// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build !darwin

package ui

import (
	"context"
	"fmt"
	"os"
)

type otherUI struct{}

func New(_ Mode) UI { return &otherUI{} }

func (u *otherUI) IsAvailable() bool { return false }

func (u *otherUI) Start(ctx context.Context, _ Handler, _, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

func (u *otherUI) Stop() {}

func (u *otherUI) Confirm(_, _ string) bool { return false }

func (u *otherUI) Notify(_, message string) {
	fmt.Fprintln(os.Stderr, message)
}

func (u *otherUI) ShowLog(title, message string, logSnippet []byte, logPath string) {
	fmt.Fprintf(os.Stderr, "%s: %s\n\n%s\n\nFull log: %s\n", title, message, string(logSnippet), logPath)
}
