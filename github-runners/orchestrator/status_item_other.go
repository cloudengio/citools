// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build !darwin

package main

import "context"

func isGUIAvailable() bool {
	return false
}

func startStatusItem(_ context.Context, _ context.CancelFunc, _, _ string) {}

func stopStatusItem() {}
