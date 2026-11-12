# Package [github.com/cloudengio/citools/runners/macos/orchestrator/githubclient](https://pkg.go.dev/github.com/cloudengio/citools/runners/macos/orchestrator/githubclient?tab=doc)

```go
import github.com/cloudengio/citools/runners/macos/orchestrator/githubclient
```


## Constants
### OutcomeSuccess, OutcomeFailure
```go
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
)
```
The outcomes the orchestrator files finished jobs under. A job that failed
is worth keeping for longer than one that succeeded, which is why they are
separate queues with retention periods of their own.



## Variables
### CompletionOutcomes
```go
CompletionOutcomes = []string{OutcomeSuccess, OutcomeFailure}

```
CompletionOutcomes lists the outcomes configured for finished jobs.

### ErrWorkflowNotRunning
```go
ErrWorkflowNotRunning = errors.New("no such running workflow")

```
ErrWorkflowNotRunning is returned by Cancel when no live workflow with the
given name exists (it may already have completed).



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

### Func WithCompletionQueue
```go
func WithCompletionQueue(size int, successfulRetention, failedRetention time.Duration) vmsclient.Option
```
WithCompletionQueue configures the queue the orchestrator parks finished
jobs in: one outcome for a job that succeeded and one for a job that failed,
each holding at most size of them for its own retention period.

It is an option for the VM pools because they are what creates the queue
and closes it: every VM they hand out carries it, which is how a finished
job reaches it, and closing them releases the VMs it is still holding.
The handler fills and drains it, see vmsclient.Pools.CompletionQueue;
a pool has no notion of a job being finished.



## Types
### Type CompletionQueue
```go
type CompletionQueue = vmspool.CompletionQueue
```
CompletionQueue parks finished jobs, and the VMs they ran on, so that a VM
can be inspected before it is deleted, see vmspool.CompletionQueue.


### Type JobStartedInfo
```go
type JobStartedInfo struct {
	RunID           int64             `json:"run_id,omitempty"`
	RunNumber       int64             `json:"run_number,omitempty"`
	RunAttempt      int64             `json:"run_attempt,omitempty"`
	Job             string            `json:"job,omitempty"`
	Workflow        string            `json:"workflow,omitempty"`
	Repository      string            `json:"repository,omitempty"`
	RepositoryOwner string            `json:"repository_owner,omitempty"`
	EventName       string            `json:"event_name,omitempty"`
	SHA             string            `json:"sha,omitempty"`
	Ref             string            `json:"ref,omitempty"`
	Actor           string            `json:"actor,omitempty"`
	RunnerName      string            `json:"runner_name,omitempty"`
	Raw             map[string]string `json:"raw,omitempty"`
}
```
JobStartedInfo contains metadata captured by the
ACTIONS_RUNNER_HOOK_JOB_STARTED hook when GitHub assigns a workflow run to
the runner.

### Methods

```go
func (j *JobStartedInfo) UnmarshalJSON(data []byte) error
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
func (c *Repo) EnsureJobCompletedOrCanceled(ctx context.Context, runID, jobID int64, jobName string) error
```


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
func (rc *RepoClients) EnsureJobCompletedOrCanceled(ctx context.Context, fullName string, runID, jobID int64, jobName string) error
```
EnsureJobCompletedOrCanceled checks the job status on GitHub. If the job is
not yet completed or canceled, it requests cancellation of the workflow run
and verifies that GitHub reports the job as completed/canceled.


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
func NewWorkflowEventHandler(ctx context.Context, tmpDir string, statusRetention time.Duration, poolConfigs map[string]vmsclient.PoolConfig, repoConfigs []RepositoryConfig, clients *RepoClients, poolOpts ...vmsclient.Option) (*WorkflowEventHandler, error)
```
NewWorkflowEventHandler creates the handler, and with it the VM pools the
jobs it handles run on. poolOpts configures those pools; the orchestrator
passes WithCompletionQueue, so that the VMs of finished jobs are parked
rather than deleted at once, and a handler created without it deletes each
VM as the job on it finishes.



### Methods

```go
func (r *WorkflowEventHandler) Cancel(ctx context.Context, name string) error
```
Cancel cancels a running workflow job by runner-instance name. It cancels
the job's GitHub workflow run; the resulting "completed" webhook then tears
the job's VM down through the normal completion path (see handleCompleted).
It returns ErrWorkflowNotRunning if no live instance exists for name.


```go
func (r *WorkflowEventHandler) Close(ctx context.Context) error
```


```go
func (r *WorkflowEventHandler) CompletionQueueVMIDs() map[string]bool
```
CompletionQueueVMIDs returns the set of VM IDs currently in the completion
queue, as a set so that the ui can ask about each VM it is listing.


```go
func (r *WorkflowEventHandler) DrainCompletionQueue(ctx context.Context, waitForInput bool)
```


```go
func (r *WorkflowEventHandler) HandleWebhooks(ctx context.Context, event *gogithub.WorkflowJobEvent) error
```


```go
func (r *WorkflowEventHandler) HasCapacity() bool
```
HasCapacity reports whether the VM pools can supply a VM for a queued job,
see vmsclient.Pools.HasCapacity. It is what gates the webhook listener's
connection to the relay: there is no point in holding it, or in reading
events that would only queue up here, whilst no job can be started.

A handler with no pools has no capacity, since a job has nothing to run on,
which leaves the events at the relay for an orchestrator that does.


```go
func (r *WorkflowEventHandler) PoolStatus(ctx context.Context) ([]vmsclient.PoolSnapshot, error)
```
PoolStatus returns a snapshot of every configured VM pool and its VMs.


```go
func (r *WorkflowEventHandler) RefreshPool(ctx context.Context, name string) (int, error)
```
RefreshPool removes all currently available VMs in the named pool and
replaces them with newly cloned ones. Acquired (busy) VMs are not affected.


```go
func (r *WorkflowEventHandler) RunJob(ctx context.Context, owner, repo string, labels []string, waitForUserInput bool) error
```


```go
func (r *WorkflowEventHandler) Subscribe(ctx context.Context) (<-chan struct{}, func())
```
Subscribe returns a coalescing change signal that fires when either pool,
workflow, or completion queue state changes, plus a cancel function that
must be called to release all underlying subscriptions. The subscriptions
are also released when ctx is cancelled.


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
	JobStarted                  *JobStartedInfo
	JobStartedRaw               []byte
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
func (wi *WorkflowInstance) ExtractLogs(ctx context.Context) error
```
ExtractLogs extracts job-started info and diagnostic logs from the VM.
It is guaranteed to run at most once, and always before the VM is stopped.


```go
func (wi *WorkflowInstance) GetLogger(logger *slog.Logger) *slog.Logger
```
GetLogger implements vmspool.CompletedVM, returning a logger enriched with
workflow instance details.


```go
func (wi *WorkflowInstance) GetVM() *vmspool.VM
```
GetVM implements vmspool.CompletedVM, returning the VM associated with this
workflow instance, or nil if it never acquired one.


```go
func (wi *WorkflowInstance) RunJob(ctx context.Context, clients *RepoClients, status *statusTracker) error
```
RunJob runs the job on the instance's VM and returns the local outcome
(nil on success). The VM has finished running by the time this returns.
It examines the jobStarted data reported by the VM's start hook, compares it
with the workflow instance values (reporting errors to logs and UI if they
differ), and ensures that the job status on GitHub is completed or canceled.
The job is parked in the completion queue of the pool its VM came from,
see vmspool.VM.CompletionQueue, so the queue is not a parameter: the VM is.


```go
func (wi *WorkflowInstance) StopAndReleaseVM(ctx context.Context, timeout time.Duration) (error, error)
```
StopAndReleaseVM ensures diagnostic logs are extracted before the VM is
stopped, and then stops and releases the VM. It guarantees StopAndRelease is
called at most once, and never concurrently with log extraction.




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
	RunID        int64
	JobURL       string
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
	JobStarted    *JobStartedInfo
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
const (
	WorkflowQueued    WorkflowState = "queued"
	WorkflowAcquiring WorkflowState = "acquiring"
	WorkflowRunning   WorkflowState = "running"
	// WorkflowVMCompleted means the job finished on the local VM (the runner
	// exited and the VM was stopped) but GitHub has not yet acknowledged
	// completion via a workflow_job "completed" webhook.
	WorkflowVMCompleted WorkflowState = "vm_completed"
	// WorkflowCompleted means GitHub has delivered the workflow_job "completed"
	// webhook, i.e. the run is finished end-to-end.
	WorkflowCompleted WorkflowState = "completed"
	WorkflowFailed    WorkflowState = "failed"
	WorkflowCanceled  WorkflowState = "canceled"
)
```







