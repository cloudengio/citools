// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
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
