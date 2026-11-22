// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build unix

package main

import (
	"os"
	"os/signal"
	"syscall"

	"cloudeng.io/net/pssh"
)

// watchWindowSize returns the size, as reported by size, each time the
// terminal is resized until stop is closed.
func watchWindowSize(stop <-chan struct{}, size func() pssh.WindowSize) <-chan pssh.WindowSize {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGWINCH)
	changes := make(chan pssh.WindowSize)
	go func() {
		defer signal.Stop(sigs)
		for {
			select {
			case <-stop:
				return
			case <-sigs:
			}
			select {
			case changes <- size():
			case <-stop:
				return
			}
		}
	}()
	return changes
}
