# Package [github.com/cloudengio/citools/runners/macos/orchestrator/internal](https://pkg.go.dev/github.com/cloudengio/citools/runners/macos/orchestrator/internal?tab=doc)

```go
import github.com/cloudengio/citools/runners/macos/orchestrator/internal
```


## Constants
### OrchestratorBinary, BundleID, ConfigDir, ConfigDirXDG, ConfigFileName, LaunchAgentFileName, RunLockName
```go
const (
	// OrchestratorBinary is the name of the orchestrator executable.
	OrchestratorBinary = "github-runner-orchestrator"

	// BundleID is the orchestrator bundle's CFBundleIdentifier, matching the
	// App ID and provisioning profile that carry the keychain entitlement.
	// It is also the launchd label of the login service.
	BundleID = "io.cloudeng." + OrchestratorBinary

	// ConfigDir is the per-user directory, within the directory reported by
	// os.UserConfigDir, holding the configuration file and the run lock.
	ConfigDir = "io.cloudeng." + OrchestratorBinary

	// ConfigDirXDG is the directory, within ~/.config, that the configuration
	// file is also searched for in.
	ConfigDirXDG = "github-orchestrator"

	// ConfigFileName is the name of the configuration file, both in ConfigDir
	// and in the bundle's Resources directory.
	ConfigFileName = "config.yaml"

	// LaunchAgentFileName is the name of the launchd login service
	// configuration, both in the source tree and in the bundle's Resources.
	LaunchAgentFileName = "launch_agent.yml"

	// RunLockName is the name of the single-instance run lock in ConfigDir.
	RunLockName = "run.lock"
)
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




### Type TimestampOption
```go
type TimestampOption func(*TimestampWriter)
```
TimestampOption configures a TimestampWriter.

### Functions

```go
func WithTimestampClock(now func() time.Time) TimestampOption
```
WithTimestampClock sets the function that supplies the time, which defaults
to time.Now. It is intended for tests.


```go
func WithTimestampLayout(layout string) TimestampOption
```
WithTimestampLayout sets the time layout used for the prefix, which defaults
to time.RFC3339.




### Type TimestampWriter
```go
type TimestampWriter struct {
	// contains filtered or unexported fields
}
```
TimestampWriter wraps a writer so that every line written to it is
prefixed with the time at which the line was completed. It is used for
the orchestrator's standard error, which the login service captures in its
.err.log and which is read long after the fact, so that a multi-line message
says when it was written on each of its lines rather than only the first.

### Functions

```go
func NewTimestampWriter(out io.Writer, opts ...TimestampOption) *TimestampWriter
```
NewTimestampWriter returns a TimestampWriter that writes to out.



### Methods

```go
func (w *TimestampWriter) Flush() error
```
Flush writes any partial line that is still held back, terminating it with
a newline. It is called before exiting, since a message written without a
trailing newline would otherwise be lost.


```go
func (w *TimestampWriter) Write(p []byte) (int, error)
```
Write implements io.Writer. Every complete line is written to the underlying
writer prefixed with the time and a single space; a blank line is left bare,
so that the blank lines separating the parts of a message still read as
such. A trailing partial line is held back until the write that completes
it, or until Flush, since the timestamp is of no use half way along a line.







