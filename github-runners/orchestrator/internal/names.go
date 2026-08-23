// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package internal

// The names and identifiers shared by the orchestrator, the launcher and the
// bundle command. The launcher locates the orchestrator, its config and its
// login service inside the .app that the bundle command builds, so the two must
// agree on every one of these; defining them once here is what keeps them in
// step. They are derived from OrchestratorBinary rather than repeated so that
// renaming the tool is a single edit.
const (
	// OrchestratorBinary is the name of the orchestrator executable.
	OrchestratorBinary = "github-runner-orchestrator"

	// LauncherBinary is the name of the .app launcher executable, the outer
	// bundle's CFBundleExecutable.
	LauncherBinary = OrchestratorBinary + "-launcher"

	// NestedOrchestratorApp is the name of the nested bundle, within the outer
	// app's Contents/Library, that holds the orchestrator.
	NestedOrchestratorApp = OrchestratorBinary + ".app"

	// BundleID is the nested bundle's CFBundleIdentifier, matching the App ID
	// and provisioning profile that carry the keychain entitlement. It is also
	// the launchd label of the login service.
	BundleID = "io.cloudeng." + OrchestratorBinary

	// OuterBundleID is the identifier of the outer launcher app. It must differ
	// from BundleID and needs no provisioning profile.
	OuterBundleID = BundleID + ".app"

	// ConfigDir is the per-user directory, within the directory reported by
	// os.UserConfigDir, holding the configuration file and the run lock.
	ConfigDir = OrchestratorBinary

	// ConfigFileName is the name of the configuration file, both in ConfigDir
	// and in the bundle's Resources directory.
	ConfigFileName = "github_orchestrator_config.yml"

	// LaunchAgentFileName is the name of the launchd login service
	// configuration, both in the source tree and in the bundle's Resources.
	LaunchAgentFileName = "launch_agent.yml"

	// RunLockName is the name of the single-instance run lock in ConfigDir.
	RunLockName = "run.lock"
)
