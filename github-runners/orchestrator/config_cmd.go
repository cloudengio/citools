// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"

	"cloudeng.io/cmdutil/structdoc"
	"gopkg.in/yaml.v3"
)

type ConfigCommand struct{}

func (c ConfigCommand) Show(ctx context.Context, _ any, _ []string) error {
	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return fmt.Errorf("no config in context")
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("error formatting config to yaml: %v", err)
	}
	fmt.Println(string(out))
	return nil
}

func (c ConfigCommand) Describe(ctx context.Context, _ any, _ []string) error {
	cfg, ok := ConfigFromContext(ctx)
	if !ok {
		return fmt.Errorf("no config in context")
	}
	desc, err := structdoc.Describe(cfg, "doc", "root configuration options\n")
	if err != nil {
		return fmt.Errorf("error describing config: %v", err)
	}
	fmt.Println(desc)
	return nil
}
