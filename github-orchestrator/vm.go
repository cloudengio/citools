// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
)

type VMInfo struct {
	Address string
}

type VM interface {
	Clone(ctx context.Context, vm VMConfig) error
	Start(ctx context.Context, vm VMConfig) error
	RunInfo(ctx context.Context, vm VMConfig) (VMInfo, error)
	Stop(ctx context.Context, vm VMConfig) error
	Delete(ctx context.Context, vm VMConfig) error
}
