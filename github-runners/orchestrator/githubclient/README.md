# Package [github.com/cloudengio/citools/runners/macos/orchestrator/githubclient](https://pkg.go.dev/github.com/cloudengio/citools/runners/macos/orchestrator/githubclient?tab=doc)

```go
import github.com/cloudengio/citools/runners/macos/orchestrator/githubclient
```


## Functions
### Func LogEventGroup
```go
func LogEventGroup(event *gogithub.WorkflowJobEvent) slog.Attr
```

### Func LogJobGroup
```go
func LogJobGroup(job *gogithub.WorkflowJob) slog.Attr
```

### Func LogRepoGroup
```go
func LogRepoGroup(repo *gogithub.Repository) slog.Attr
```

### Func LogRunnerConfigGroup
```go
func LogRunnerConfigGroup(runner *RunnerConfig) slog.Attr
```

### Func LogWorkflowInstanceGroup
```go
func LogWorkflowInstanceGroup(inst *WorkflowInstance) slog.Attr
```

### Func LoggerWithEvent
```go
func LoggerWithEvent(logger *slog.Logger, event *gogithub.WorkflowJobEvent) *slog.Logger
```

### Func LoggerWithWorkflowInstance
```go
func LoggerWithWorkflowInstance(logger *slog.Logger, inst *WorkflowInstance) *slog.Logger
```



## Types
### Type CompletionQueue
```go
type CompletionQueue = vmsclient.CompletionQueue[WorkflowInstance]
```

### Functions

```go
func NewCompletionQueue(ctx context.Context, size int, successfulRetention, failedRetention time.Duration) *CompletionQueue
```




### Type Repo
```go
type Repo struct {
	// contains filtered or unexported fields
}
```
Repo provides a client for GitHub repository operations. There will be a
separate client for each repository that is being managed.

### Functions

```go
func New(owner, repo string, opts ...operations.Option) *Repo
```



### Methods

```go
func (c *Repo) GetRegistrationToken(ctx context.Context) (*gogithub.RegistrationToken, error)
```


```go
func (c *Repo) GetWorkflowJob(ctx context.Context, jobID int64) (*gogithub.WorkflowJob, error)
```




### Type RepoClients
```go
type RepoClients struct {
	// contains filtered or unexported fields
}
```

### Functions

```go
func NewRepoClients() *RepoClients
```



### Methods

```go
func (rc *RepoClients) AddClient(owner, repo string, opts ...operations.Option) *Repo
```


```go
func (rc *RepoClients) CancelWorkflowRunFullName(ctx context.Context, fullName string, runID int64) error
```


```go
func (rc *RepoClients) GetClient(owner, repo string) (*Repo, bool)
```


```go
func (rc *RepoClients) GetClientFullName(fullName string) (*Repo, bool)
```


```go
func (rc *RepoClients) GetToken(ctx context.Context, owner, repo string) (*gogithub.RegistrationToken, error)
```


```go
func (rc *RepoClients) GetTokenFullName(ctx context.Context, fullName string) (*gogithub.RegistrationToken, error)
```


```go
func (rc *RepoClients) GetWorkflowJobFullName(ctx context.Context, fullName string, jobID int64) (*gogithub.WorkflowJob, error)
```


```go
func (rc *RepoClients) RerunWorkflowJobFullName(ctx context.Context, fullName string, jobID int64) error
```




### Type RepositoryConfig
```go
type RepositoryConfig struct {
	apicrawlcmd.Crawl[githubcmd.Service] `yaml:",inline"`
	Runners                              []RunnerConfig `yaml:"runners" doc:"list of runner configurations for this repository"`
}
```

### Methods

```go
func (rc *RepositoryConfig) UnmarshalYAML(node *yaml.Node) error
```
UnmarshalYAML implements yaml.Unmarshaler. The embedded
apicrawlcmd.Crawl[githubcmd.Service] expects the GitHub service fields to
be nested under a "service_config" key and the API key under "key_id", but
the orchestrator config lists them flat within each repository entry (owner,
organization, repo, per_page, api_key_id). This method accepts the flat
layout while still honoring the nested Crawl form when present.


```go
func (rc RepositoryConfig) Validate() error
```




### Type RunnerConfig
```go
type RunnerConfig struct {
	NamePrefix string `yaml:"name_prefix" doc:"prefix for name of the runner, must be unique within runners and will be suffixed with a timestamp and incrementing number to ensure uniqueness"`

	VMPoolName string `yaml:"vm_pool" doc:"name of the VM pool to use for this runner"`

	Labels []string `yaml:"labels,flow" doc:"labels to assign to the runner, used to match runner to webhook events"`

	Replace   bool          `yaml:"replace" doc:"if true, replace any existing runner with the same name"`
	Ephemeral bool          `yaml:"ephemeral" doc:"if true, register the runner as ephemeral"`
	Timeout   time.Duration `yaml:"timeout" doc:"maximum time to wait for the runner to complete a job before it is considered failed and terminated"`
}
```

### Methods

```go
func (rc RunnerConfig) Validate() error
```




### Type WorkflowEventHandler
```go
type WorkflowEventHandler struct {
	// contains filtered or unexported fields
}
```

### Functions

```go
func NewWorkflowEventHandler(ctx context.Context, tmpDir string, cq *CompletionQueue, statusRetention time.Duration, poolConfigs map[string]vmsclient.PoolConfig, repoConfigs []RepositoryConfig, clients *RepoClients) (*WorkflowEventHandler, error)
```



### Methods

```go
func (r *WorkflowEventHandler) Close(ctx context.Context) error
```


```go
func (r *WorkflowEventHandler) DrainCompletionQueue(ctx context.Context, waitForInput bool)
```


```go
func (r *WorkflowEventHandler) HandleWebhooks(ctx context.Context, event *gogithub.WorkflowJobEvent) error
```


```go
func (r *WorkflowEventHandler) PoolStatus(ctx context.Context) ([]vmsclient.PoolSnapshot, error)
```
PoolStatus returns a snapshot of every configured VM pool and its VMs.


```go
func (r *WorkflowEventHandler) RunJob(ctx context.Context, owner, repo string, labels []string, waitForUserInput bool) error
```


```go
func (r *WorkflowEventHandler) Subscribe(ctx context.Context) (<-chan struct{}, func())
```
Subscribe returns a coalescing change signal that fires when either pool
or workflow state changes, plus a cancel function that must be called to
release both underlying subscriptions. The subscriptions are also released
when ctx is cancelled.


```go
func (r *WorkflowEventHandler) Workflow(name string) (WorkflowSnapshot, bool)
```
Workflow returns the snapshot for a single workflow job by runner-instance
name.


```go
func (r *WorkflowEventHandler) Workflows() []WorkflowSnapshot
```
Workflows returns a snapshot of every running and recently-completed
workflow job tracked by the orchestrator.




### Type WorkflowInstance
```go
type WorkflowInstance struct {
	Name                        string
	RunStdoutStderr, DiagStdout io.Writer
	LogName, DiagName           string
	RunnerConfig                *RunnerConfig
	PoolConfig                  *vmsclient.PoolConfig
	Event                       *gogithub.WorkflowJobEvent
	RepoURL                     string
	// contains filtered or unexported fields
}
```
WorkflowInstance represents a single instance of a workflow executed on a
VM.

### Methods

```go
func (wi *WorkflowInstance) AcquireVMAndToken(ctx context.Context, pools *vmsclient.Pools, repoClients *RepoClients) error
```
AcquireVMAndToken acquires a VM and github registration token concurrently.


```go
func (wi *WorkflowInstance) Close(ctx context.Context)
```


```go
func (wi WorkflowInstance) GetLogger(logger *slog.Logger) *slog.Logger
```
GetLogger implements the CompletionEventPayload interface, returning a
logger enriched with workflow instance details.


```go
func (wi WorkflowInstance) GetVM() *vmspool.VM
```
GetVM implements the CompletionEventPayload interface, returning the VM
associated with this workflow instance.


```go
func (wi *WorkflowInstance) RunJob(ctx context.Context, cq *CompletionQueue) error
```
RunJob runs the job on the instance's VM and returns the local outcome (nil
on success). The VM has finished running by the time this returns.




### Type WorkflowSnapshot
```go
type WorkflowSnapshot struct {
	Name         string
	State        WorkflowState
	RepoFullName string
	RepoURL      string
	WorkflowName string
	JobName      string
	JobID        int64
	Labels       []string
	Pool         string
	VMID         string
	Result       string
	Err          string
	QueuedAt     time.Time
	StartedAt    time.Time
	// VMCompletedAt is when the job finished on the local VM (state
	// vm_completed); CompletedAt is when GitHub acknowledged completion.
	VMCompletedAt time.Time
	CompletedAt   time.Time
	JobLogPath    string
	DiagLogPath   string
}
```
WorkflowSnapshot is a point-in-time view of a single workflow job tracked
by the orchestrator. It is a flat, serialization-friendly projection of a
WorkflowInstance plus its lifecycle state and timestamps.


### Type WorkflowState
```go
type WorkflowState string
```
WorkflowState is the lifecycle state of a workflow job within the
orchestrator. It intentionally mirrors the API's WorkflowState enum.

### Constants
### WorkflowQueued, WorkflowAcquiring, WorkflowRunning, WorkflowVMCompleted, WorkflowCompleted, WorkflowFailed, WorkflowCanceled
```go
WorkflowQueued WorkflowState = "queued"
WorkflowAcquiring WorkflowState = "acquiring"
WorkflowRunning WorkflowState = "running"
// WorkflowVMCompleted means the job finished on the local VM (the runner
// exited and the VM was stopped) but GitHub has not yet acknowledged
// completion via a workflow_job "completed" webhook.
WorkflowVMCompleted WorkflowState = "vm_completed"
// WorkflowCompleted means GitHub has delivered the workflow_job "completed"
// webhook, i.e. the run is finished end-to-end.
WorkflowCompleted WorkflowState = "completed"
WorkflowFailed WorkflowState = "failed"
WorkflowCanceled WorkflowState = "canceled"

```







