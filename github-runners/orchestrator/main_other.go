// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build !darwin

package main

import (
	"context"

	"cloudeng.io/cmdutil/keys"
	"cloudeng.io/cmdutil/subcmd"
)

func withKeysPrehook(ctx context.Context) (context.Context, string, subcmd.PostHook, error) {
	id := "withConfigPrehook"
	postHook := func(ctx context.Context) (string, error) { return id, nil }
	ctx = keys.ContextWithKeyStore(ctx, keys.NewInMemoryKeyStore())
	return ctx, id, postHook, nil
}

type ICloudKeychainConfig struct{}
