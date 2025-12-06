// Copyright 2025 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

type GitHubConfig struct {
}

type VMConfig struct {
	VMType string `yaml:"vm_type"`
	Image  string `yaml:"vm_image"`
	Name   string `yaml:"vm_name"`
	User   string `yaml:"vm_user"`
	NumVMs int    `yaml:"vm_num_vms"`
}

type Config struct {
	GitHub GitHubConfig `yaml:"github"`
	VM     VMConfig     `yaml:"vm"`
}
