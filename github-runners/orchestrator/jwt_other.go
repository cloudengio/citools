// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build !darwin

package main

import (
	"context"
	"fmt"

	"cloudeng.io/file"
)


func getKeychainReadFileFS(ctx context.Context) (file.ReadFileFS, error) {
	if rwfs, ok := file.ReadWriteFSFromContext(ctx); ok {
		return rwfs, nil
	}
	return nil, fmt.Errorf("keychain reading is only supported on macOS")
}
