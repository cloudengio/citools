# macOS UI Architecture & Design

This document details the architecture and design of the macOS user interface subsystem for the GitHub Runner Orchestrator.

---

## 1. Overview & Objectives

The orchestrator UI provides a seamless desktop experience on macOS while simultaneously serving as a headless or background daemon. The design achieves four core goals:

1. **Unified Executable:** A single binary (`github-runner-orchestrator`) serves both as an interactive desktop application (with a Dock icon and menu bar status extra) and as a background login service managed by `launchd` (menu bar status extra only).
2. **Native Dialogs & In-Process Cocoa:** Replaced external `osascript` alerts with native AppKit `NSAlert` modal dialogs featuring a scrollable monospace log viewer. This resolves window focus conflicts and inoperative dialog buttons on modern macOS releases.
3. **Clean Decoupling:** All Darwin/AppKit Cgo code is isolated behind the Go `internal/ui` interface package. Non-Darwin platforms compile with lightweight stubs and no Cgo dependency.
4. **Single Application Bundle:** Eliminated nested `.app` bundles, halving distribution size and simplifying code signing and provisioning profile entitlements.

---

## 2. Architecture & The `internal/ui` Package

The UI subsystem is encapsulated within [`internal/ui`](internal/ui/). The package defines a pure Go interface, accompanied by a Darwin AppKit implementation (`ui_darwin.go`, `ui_darwin.m`) and a no-op stub for other platforms (`ui_other.go`).

### Interface Definitions

```go
type Mode int

const (
    // ModeAccessory hides the Dock icon (NSApplicationActivationPolicyAccessory).
    // Used when running as a background login service.
    ModeAccessory Mode = iota

    // ModeRegular displays a standard Dock icon (NSApplicationActivationPolicyRegular).
    // Used when running interactively or launched from Finder/Dock.
    ModeRegular
)

type UI interface {
    Start(ctx context.Context, handler Handler, webURL, logPath string) error
    Stop()
    IsAvailable() bool
    Dialogs() Dialogs
}

type Handler interface {
    OnOpenWebUI()
    OnViewLogs()
    IsServiceInstalled() bool
    OnInstallService()
    OnRestartService()
    OnUninstallService()
    OnQuit()
}

type Dialogs interface {
    Notify(title, message string)
    Confirm(title, message string) bool
    ErrorWithLog(title, message, logPath string, logTailLines int) bool
}
```

### Threading & Cgo Boundary

- **Main Thread Locking:** Cocoa/AppKit requires that UI operations and the `NSApplication` run loop execute on the OS main thread. `main.go` calls `runtime.LockOSThread()` before launching CLI commands.
- **Asynchronous Event Dispatch:** When Go routines invoke UI methods (such as `u.Stop()` or dialog alerts), the Objective-C layer dispatches them onto the main queue via `dispatch_async(dispatch_get_main_queue(), ^{ ... })` or `dispatch_sync`.
- **Decoupled Callbacks:** Cgo delegates store an opaque Go pointer context (`uintptr`) rather than relying on package-level global variables, ensuring clean encapsulation and testability.

---

## 3. Execution Modes & Lifecycle

| Aspect | Regular Mode (`ModeRegular`) | Accessory Mode (`ModeAccessory`) | Headless Mode |
| :--- | :--- | :--- | :--- |
| **Activation Policy** | `NSApplicationActivationPolicyRegular` | `NSApplicationActivationPolicyAccessory` | N/A |
| **Dock Icon** | Visible with application icon | Hidden | Hidden |
| **Menu Bar Icon** | Visible (`NSStatusItem`) | Visible (`NSStatusItem`) | None (`--no-menu-bar`) |
| **Subcommand** | `launch` | `run` | `run --no-menu-bar` |
| **Typical Context** | User launches `.app` from Finder / Dock | LaunchAgent login service at login | Terminal CLI / CI / headless host |

### Launch Detection & Dispatch

When a user double-clicks the application bundle in macOS Finder or launches it from the Dock:
1. The bundle executable is launched with no arguments (`len(os.Args) == 1`).
2. `main.go` detects that the process is executing inside a macOS bundle using `macosutils.ProcessInBundle()`.
3. It automatically rewrites the execution arguments to invoke the `launch` subcommand (`LaunchCommand`).
4. `LaunchCommand`:
   - Checks if a minimal configuration exists; if not, seeds `~/Library/Application Support/io.cloudeng.github-runner-orchestrator/github_orchestrator_config.yml`.
   - Checks if the login service is already installed and running. If so, opens the Web UI in the default browser and exits.
   - If not installed, prompts the user with a native modal dialog (`Confirm`): *"Start the GitHub Runner Orchestrator automatically when you log in?"*
   - If the user confirms, installs and loads the LaunchAgent login service.
   - If the user declines, runs the orchestrator in-process with a Dock icon (`ModeRegular`) and menu bar item.

### Clean Shutdown

When the user selects **Quit** from the menu bar:
1. AppKit calls the Objective-C `onQuit:` selector.
2. The selector invokes Go's `Handler.OnQuit()`.
3. The handler cancels the orchestrator's root `runCtx` context.
4. The GitHub webhook relay listener and HTTP servers drain and exit.
5. `isCleanShutdown(err)` detects that termination was user-initiated (`context.Canceled`) and exits cleanly with exit code `0`.

---

## 4. Menu Bar Status Item (`NSStatusItem`)

The orchestrator registers a status item with variable length in the macOS Menu Extra bar displaying a runner icon or title. Clicking the icon presents a native `NSMenu` with dynamic state:

- **Open Web UI:** Launches the default web browser targeting the configured Web UI listen address (`http://127.0.0.1:<port>`). Disabled if Web UI is disabled in configuration.
- **View Logs:** Opens the service output log file using the default macOS handler (`Console.app` or default viewer).
- *[Separator]*
- **Service Controls (Dynamic):**
  - If installed:
    - **Restart Service:** Restarts the background LaunchAgent via `launchctl kickstart -k`.
    - **Uninstall Service:** Unloads and removes the login service plist.
  - If not installed:
    - **Install Service:** Configures and registers the LaunchAgent login service.
- *[Separator]*
- **Quit:** Initiates graceful shutdown.

---

## 5. Native Modal Dialogs & Log Viewer

### Why `osascript` Was Replaced

Previously, UI notifications and confirmations used AppleScript executed via `osascript -e 'display alert ...'`. In modern macOS (including macOS 15+ Sequoia and Tahoe), spawning an external `osascript` process from an active Cocoa application causes the WindowServer to treat the resulting modal window as backgrounded or unassociated with the caller. This triggered macOS click-through protection, rendering the dialog buttons (such as the "OK" button) unresponsive to user clicks.

### Native AppKit Dialog Implementation

All dialogs now run in-process on the AppKit main dispatch queue using `NSAlert`:

- **Standard Alerts (`Notify`, `Confirm`):** Run modally via `[alert runModal]`, ensuring proper window layering, keyboard navigation (Enter/Escape), and responsive buttons.
- **Error Dialogs with Scrollable Log (`ErrorWithLog`):**
  - When an error occurs or the process fails to start, `ErrorWithLog` extracts the tail of the log file.
  - The alert installs a custom `NSScrollView` accessory view containing a read-only `NSTextView`.
  - The log snippet is rendered using the system fixed-pitch font (`[NSFont userFixedPitchFontOfSize:11.0]`) with line wrapping disabled and horizontal scrolling enabled.
  - An **"Open Full Log"** button opens the complete log file in macOS Console or the default editor via `[[NSWorkspace sharedWorkspace] openURL:]`.

---

## 6. Single App Bundle Architecture

The application bundle packaging (`orchestrator bundle`) builds a single, unnested macOS application:

```
github-runner-orchestrator.app/
  Contents/
    Info.plist
    MacOS/
      github-runner-orchestrator          <- Universal binary (regular & accessory)
    Resources/
      github_orchestrator_config.yml      <- Default initial configuration
      launch_agent.yml                    <- LaunchAgent service configuration template
    embedded.provisionprofile            <- Developer ID profile with Keychain Sharing
```

### Key Advantages

1. **Size Efficiency:** Eliminating the secondary nested launcher `.app` bundle halved the total bundle footprint (~40 MB instead of ~80 MB).
2. **Simplified Entitlements:** The restricted `keychain-access-groups` entitlement is applied directly to the main executable and authorized by `embedded.provisionprofile`. Under Apple Mobile File Integrity (AMFI), provisioning profiles are validated for the main bundle executable, eliminating nested bundle permission friction.
3. **Unified Signing:** Code signing seals the single bundle in a single pass without needing to sign inner nested bundles before outer bundles.
