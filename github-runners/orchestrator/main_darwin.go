// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build darwin

package main

import (
	"context"
	"fmt"

	"cloudeng.io/cmdutil/keys"
	"cloudeng.io/cmdutil/subcmd"
	"cloudeng.io/logging/ctxlog"
	macoskeychain "cloudeng.io/macos/keychain/plugin"
)

type ICloudKeychainConfig struct {
	macoskeychain.Config `yaml:",inline"`
	Items                []string `yaml:"items" doc:"list of keychain item names to use for API keys (the value of the keychain items should be in cloudeng.io/cmdutil/keys.Info format)"`
}

func withKeysPrehook(ctx context.Context) (context.Context, string, subcmd.PostHook, error) {
	id := "withConfigPrehook"
	postHook := func(ctx context.Context) (string, error) { return id, nil }

	ks := keys.NewInMemoryKeyStore()
	ctx = keys.ContextWithKeyStore(ctx, ks)
	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return ctx, id, postHook, fmt.Errorf("no config in context")
	}

	if len(cfg.ICloudKeychain.Items) > 0 {
		var err error
		ctx, err = loadKeychain(ctx, ks, cfg.ICloudKeychain)
		if err != nil {
			return ctx, id, postHook, fmt.Errorf("error loading keychain: %v", err)
		}
	}
	return ctx, id, postHook, nil
}

func loadKeychain(ctx context.Context, ims *keys.InMemoryKeyStore, cfg ICloudKeychainConfig) (context.Context, error) {

	fs, err := cfg.FS(false)
	if err != nil {
		return ctx, fmt.Errorf("error creating keychain filesystem: %v", err)
	}
	for _, item := range cfg.Items {
		if err := ims.ReadYAML(ctx, fs, item); err != nil {
			return ctx, fmt.Errorf("error reading keychain item %q: %v", item, err)
		}
		if verbose() {
			ctxlog.Info(ctx, "loaded keychain item", "item", item, "total items", ims.Len())
		}
	}
	if verbose() {
		for _, key := range ims.KeySpecs() {
			ctxlog.Info(ctx, "keychain item", "user", key.User, "id", key.ID)
		}
	}
	return ctx, nil
}
