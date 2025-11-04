// Copyright 2023 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var interval time.Duration
var total time.Duration
var initial time.Duration
var verbose bool

var errInt = errors.New("interrupted")

func now() string {
	return time.Now().Format(time.DateTime)
}

func main() {
	flag.DurationVar(&interval, "interval", time.Second, "Interval between prints")
	flag.DurationVar(&total, "total", time.Second*10, "Total duration to run")
	flag.DurationVar(&initial, "initial", 0, "Initial delay before starting")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose output")
	flag.Parse()

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel(errInt)
	}()

	files := flag.Args()
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "no files specified\n")
		os.Exit(1)
	}
	if initial > 0 {
		fmt.Printf("%v: initial delay of %v\n", now(), initial)
		time.Sleep(initial)
	}
	var wg sync.WaitGroup
	wg.Add(len(files))
	errCh := make(chan error, len(files))
	for _, file := range files {
		go func() {
			errCh <- waitForFile(ctx, file, interval, total)
			wg.Done()
		}()
	}
	wg.Wait()
	close(errCh)
	exitCode := 0
	for err := range errCh {
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitCode = 1
		}
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func waitForFile(ctx context.Context, path string, interval, total time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	ctx, cancel := context.WithTimeout(ctx, total)
	defer cancel()

	if _, err := os.Stat(path); err == nil {
		return nil
	}

	for {
		fmt.Printf("%v: waiting for file %q\n", now(), path)
		select {
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				fmt.Printf("%v: %q: exists\n", now(), path)
				return nil
			}
			if verbose {
				fmt.Printf("%v: waiting for file %q\n", now(), path)
			}
		case <-ctx.Done():
			if context.Cause(ctx) == errInt {
				return nil
			}
			return fmt.Errorf("%v: waiting for file %q: %v", now(), path, ctx.Err())
		}
	}
}
