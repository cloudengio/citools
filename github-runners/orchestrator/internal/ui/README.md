# Package [github.com/cloudengio/citools/runners/macos/orchestrator/internal/ui](https://pkg.go.dev/github.com/cloudengio/citools/runners/macos/orchestrator/internal/ui?tab=doc)

```go
import github.com/cloudengio/citools/runners/macos/orchestrator/internal/ui
```


## Types
### Type Dialogs
```go
type Dialogs interface {
	Confirm(title, message string) bool
	Notify(title, message string)
	ShowLog(title, message string, logSnippet []byte, logPath string)
}
```
Dialogs provides native alert presentation.


### Type Handler
```go
type Handler interface {
	OnOpenWebUI()
	OnViewLogs()
	OnInstallService()
	OnRestartService()
	OnUninstallService()
	OnQuit()
	IsServiceInstalled() bool
}
```
Handler receives user interactions from the UI (menu items, dock quit,
etc.).


### Type Mode
```go
type Mode int
```
Mode determines whether the application presents as a regular user-facing
application (with a Dock tile) or as an accessory/agent (menu bar only).

### Constants
### ModeAccessory, ModeRegular
```go
// ModeAccessory runs the UI as an accessory/background agent:
// it creates an NSStatusItem in the macOS menu bar but shows NO Dock icon.
ModeAccessory Mode = iota
// ModeRegular runs the UI as a standard foreground application:
// it shows a Dock icon and can also display a menu bar item or dialogs.
ModeRegular

```




### Type UI
```go
type UI interface {
	Dialogs
	Start(ctx context.Context, handler Handler, webURL, logPath string) error
	Stop()
	IsAvailable() bool
}
```
UI manages the platform UI lifecycle and dialog presentation.

### Functions

```go
func New(mode Mode) UI
```







