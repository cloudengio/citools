// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build !unix

package main

import "cloudeng.io/net/pssh"

// watchWindowSize is not supported on this platform.
func watchWindowSize(<-chan struct{}, func() pssh.WindowSize) <-chan pssh.WindowSize {
	return nil
}
