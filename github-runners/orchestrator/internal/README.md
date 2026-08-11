# Package [github.com/cloudengio/citools/runners/macos/orchestrator/internal](https://pkg.go.dev/github.com/cloudengio/citools/runners/macos/orchestrator/internal?tab=doc)

```go
import github.com/cloudengio/citools/runners/macos/orchestrator/internal
```


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







