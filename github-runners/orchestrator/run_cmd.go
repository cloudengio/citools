// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
)

type RunCommand struct{}

func (r RunCommand) Run(ctx context.Context, _ any, _ []string) error {
	return nil
}
