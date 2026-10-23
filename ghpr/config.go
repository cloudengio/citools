// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"strings"
)

// Config holds the configuration for ghpr.
type Config struct {
	Prompt  string            `yaml:"prompt,omitempty" doc:"the prompt instructions used to summarize diffs"`
	Prompts map[string]string `yaml:"prompts,omitempty" doc:"tool-specific prompt instructions, keyed by tool name (e.g. claude, agy)"`
}

// Validate checks that the configuration is valid.
func (c *Config) Validate() error {
	if len(c.Prompts) == 0 && len(strings.TrimSpace(c.Prompt)) == 0 {
		return fmt.Errorf("prompt must not be empty")
	}
	if c.Prompt != "" && len(strings.TrimSpace(c.Prompt)) == 0 {
		return fmt.Errorf("prompt must not be empty")
	}
	for tool, prompt := range c.Prompts {
		if len(strings.TrimSpace(prompt)) == 0 {
			return fmt.Errorf("prompt for %q must not be empty", tool)
		}
	}
	return nil
}

// PromptFor returns the prompt for the specified tool, falling back to Prompt
// and then defaultInstructions if unset.
func (c *Config) PromptFor(tool string) string {
	if c.Prompts != nil {
		if p, ok := c.Prompts[tool]; ok && strings.TrimSpace(p) != "" {
			return p
		}
	}
	if strings.TrimSpace(c.Prompt) != "" {
		return c.Prompt
	}
	return defaultInstructions
}

type configContextKey struct{}
type configFilesContextKey struct{}

// ContextWithConfig returns a context carrying the parsed Config.
func ContextWithConfig(ctx context.Context, cfg Config) context.Context {
	return context.WithValue(ctx, configContextKey{}, cfg)
}

// ConfigFromContext extracts the Config from the context.
func ConfigFromContext(ctx context.Context) (Config, bool) {
	if ctx == nil {
		return Config{}, false
	}
	cfg, ok := ctx.Value(configContextKey{}).(Config)
	return cfg, ok
}

// ContextWithConfigFile returns a context carrying the path of the configuration file read.
func ContextWithConfigFile(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, configFilesContextKey{}, []string{path})
}

// ContextWithConfigFiles returns a context carrying the paths of the configuration files read.
func ContextWithConfigFiles(ctx context.Context, paths ...string) context.Context {
	return context.WithValue(ctx, configFilesContextKey{}, paths)
}

// ConfigFilesFromContext extracts the configuration file paths from the context.
func ConfigFilesFromContext(ctx context.Context) ([]string, bool) {
	if ctx == nil {
		return nil, false
	}
	files, ok := ctx.Value(configFilesContextKey{}).([]string)
	return files, ok
}
