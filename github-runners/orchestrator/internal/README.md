# Package [github.com/cloudengio/citools/runners/macos/orchestrator/internal](https://pkg.go.dev/github.com/cloudengio/citools/runners/macos/orchestrator/internal?tab=doc)

```go
import github.com/cloudengio/citools/runners/macos/orchestrator/internal
```


## Constants
### OrchestratorBinary, BundleID, ConfigDir, ConfigFileName, LaunchAgentFileName, RunLockName
```go
// OrchestratorBinary is the name of the orchestrator executable.
OrchestratorBinary = "github-runner-orchestrator"
// BundleID is the orchestrator bundle's CFBundleIdentifier, matching the
// App ID and provisioning profile that carry the keychain entitlement.
// It is also the launchd label of the login service.
BundleID = "io.cloudeng." + OrchestratorBinary
// ConfigDir is the per-user directory, within the directory reported by
// os.UserConfigDir, holding the configuration file and the run lock.
ConfigDir = "io.cloudeng." + OrchestratorBinary
// ConfigFileName is the name of the configuration file, both in ConfigDir
// and in the bundle's Resources directory.
ConfigFileName = "github_orchestrator_config.yml"
// LaunchAgentFileName is the name of the launchd login service
// configuration, both in the source tree and in the bundle's Resources.
LaunchAgentFileName = "launch_agent.yml"
// RunLockName is the name of the single-instance run lock in ConfigDir.
RunLockName = "run.lock"

```
The names and identifiers shared by the orchestrator and the bundle command.
Derived from OrchestratorBinary rather than repeated so that renaming the
tool is a single edit.



## Types
### Type LogFileManager
```go
type LogFileManager struct {
	// contains filtered or unexported fields
}
```
LogFileManager manages log files for the orchestrator.

### Functions

```go
func NewLogFileManager(dir string) (*LogFileManager, error)
```
NewLogFileManager creates a new LogFileManager with a temporary directory
and a global log file.



### Methods

```go
func (l *LogFileManager) CloseGlobalLogFile()
```
CloseGlobalLogFile closes the global log file if it is was ever initialized.


```go
func (l *LogFileManager) CreateTemp(runnerName, step, ext string) (*os.File, error)
```
CreateTemp creates a temporary file in the log file manager's directory with
the specified runner name, step, and extension.


```go
func (l *LogFileManager) CreateTempFilesForJob(runnerName string) (logFile, diagLogFile *os.File, err error)
```
CreateTempFilesForJob creates two temporary files for a job: one for the job
log and one for the diagnostic log.


```go
func (l *LogFileManager) GlobalLogFile() io.Writer
```
GlobalLogFile returns the global log file writer. If the log file is not
initialized, it returns io.Discard.







