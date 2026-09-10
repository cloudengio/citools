# Package [github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient](https://pkg.go.dev/github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient?tab=doc)

```go
import github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient
```


## Constants
### DefaultMockImage
```go
DefaultMockImage = "mock"

```
DefaultMockImage is reported by a mock pool that configures no image.

### VMSPrefix
```go
VMSPrefix = "ghr-orchestrator-"

```
VMSPrefix is the prefix shared by every VM name the orchestrator generates.



## Variables
### ErrMultipleBackends
```go
ErrMultipleBackends = errors.New("more than one VM backend configured (set exactly one of tart_config or mock_config)")

```
ErrMultipleBackends is returned when a pool configuration selects more than
one VM backend, which would leave the choice of backend ambiguous.

### ErrNoBackend
```go
ErrNoBackend = errors.New("no VM backend configured (set tart_config or mock_config)")

```
ErrNoBackend is returned when a pool configuration selects no VM backend.



## Functions
### Func DeletePoolVMs
```go
func DeletePoolVMs(ctx context.Context, cfg map[string]PoolConfig, stopTimeout time.Duration) ([]string, error)
```
DeletePoolVMs deletes the VMs for every configured pool, across all
backends, returning the names deleted.

### Func ListPoolVMs
```go
func ListPoolVMs(ctx context.Context, cfg map[string]PoolConfig) ([]vmspool.VMInfo, error)
```
ListPoolVMs returns the VMs currently present for every configured pool,
across all backends. It is used to enumerate the VMs left behind by a
previous run without a live Pools instance.



## Types
### Type CompletionEvent
```go
type CompletionEvent[T CompletionEventPayload] struct {
	Err     error
	Payload T
	// contains filtered or unexported fields
}
```

### Methods

```go
func (e *CompletionEvent[T]) Expiration() time.Time
```




### Type CompletionEventPayload
```go
type CompletionEventPayload interface {
	GetVM() *vmspool.VM
	GetLogger(*slog.Logger) *slog.Logger
}
```


### Type CompletionQueue
```go
type CompletionQueue[T CompletionEventPayload] struct {
	// contains filtered or unexported fields
}
```

### Functions

```go
func NewCompletionQueue[T CompletionEventPayload](ctx context.Context, capacity int, successRetention, errorRetention time.Duration) *CompletionQueue[T]
```



### Methods

```go
func (q *CompletionQueue[T]) Close(ctx context.Context) error
```


```go
func (q *CompletionQueue[T]) Failure() <-chan CompletionEvent[T]
```


```go
func (q *CompletionQueue[T]) PushFailure(event CompletionEvent[T], err error)
```


```go
func (q *CompletionQueue[T]) PushSuccess(event CompletionEvent[T])
```


```go
func (q *CompletionQueue[T]) Success() <-chan CompletionEvent[T]
```




### Type MockConfig
```go
type MockConfig struct {
	// Image is reported in status output in place of a real base image, so
	// that a mock pool is identifiable at a glance.
	Image string `yaml:"image" doc:"name reported in place of a base image for this pool; defaults to 'mock'"`
	// RunnerDir mirrors the tart backend's setting so that the runner
	// installation commands are built the same way.
	RunnerDir string `yaml:"runner_dir" doc:"directory on the guest in which the runner is installed; only recorded, never used"`
	// Suspendable selects whether the mock VMs support suspend and resume,
	// which determines whether a pool configured with the suspended staging
	// behaviour suspends its VMs or falls back to stopping them.
	Suspendable bool `yaml:"suspendable" doc:"whether the mock VMs support suspend/resume; when false a suspended pool falls back to stopping its VMs"`
}
```
MockConfig configures the mock VM backend for a pool. The pool behaves
exactly as a real one — it creates, stages, hands out and deletes VMs,
and reports the same events — but the VMs are in-process fakes that run no
commands. It exists so that the orchestrator can be exercised end to end,
in tests and by hand, on a machine with no VM technology installed.

Anything that would be run inside the VM is recorded rather than executed,
so a mock pool reports success for any job.


### Type Pool
```go
type Pool struct {
	*vmspool.Pool
	// contains filtered or unexported fields
}
```

### Methods

```go
func (p *Pool) Name() string
```




### Type PoolConfig
```go
type PoolConfig struct {
	vmspool.Config `yaml:",inline"`
	Tart           *TartConfig `yaml:"tart_config" doc:"configure and select the tart VM backend for this pool"`
	Mock           *MockConfig `yaml:"mock_config" doc:"configure and select the mock VM backend for this pool, for exercising the orchestrator without any VM technology installed"`
}
```
PoolConfig configures a single VM pool: the generic vmspool settings plus
exactly one backend section that selects and configures the VM technology
(e.g. tart) that backs the pool. Different pools may use different backends.

### Methods

```go
func (cfg PoolConfig) RunnerDir() string
```
RunnerDir returns the guest directory in which the GitHub runner is
installed for this pool's backend, or "" if no backend is configured.


```go
func (cfg PoolConfig) Validate() error
```
Validate ensures that exactly one VM backend is configured for the pool and
that its required settings are present.




### Type PoolSnapshot
```go
type PoolSnapshot struct {
	Name     string
	Kind     string
	Image    string
	Size     int
	VMs      []vmspool.VMInfo
	Counters map[string]int
	Updated  time.Time
}
```
PoolSnapshot is a point-in-time view of a single pool: its backend, the VMs
currently backing it (the ground truth reported by the backend's Provider),
and the aggregate lifecycle counters accumulated from the pool's event
stream.


### Type PoolStatusTracker
```go
type PoolStatusTracker struct {
	// contains filtered or unexported fields
}
```
PoolStatusTracker accumulates per-pool lifecycle event counters from the
pools' event streams and notifies subscribers when the aggregate state
changes. It is safe for concurrent use.


### Type Pools
```go
type Pools struct {
	// contains filtered or unexported fields
}
```
Pools is a set of named VM pools, each backed by its own Provider so that
pools using different VM technologies can run side by side.

### Functions

```go
func NewPools(ctx context.Context, cfg map[string]PoolConfig, createFile func(string) io.Writer) (*Pools, error)
```



### Methods

```go
func (p *Pools) Acquire(ctx context.Context, name string) (*vmspool.VM, error)
```


```go
func (p *Pools) Close(ctx context.Context) error
```


```go
func (p *Pools) ClosePool(ctx context.Context, name string) error
```


```go
func (p *Pools) Status(ctx context.Context) ([]PoolSnapshot, error)
```
Status returns a snapshot of every configured pool, combining the per-VM
ground truth from each pool's backend Provider with the aggregate lifecycle
counters observed on the pool's event stream. Errors from individual
backends are accumulated so that one failing pool does not hide the others.


```go
func (p *Pools) Subscribe(ctx context.Context) (<-chan struct{}, func())
```
Subscribe returns a change-signal channel and a cancel function that must be
called to release the subscription. The subscription is also released when
ctx is cancelled.




### Type Provider
```go
type Provider interface {
	vmspool.Provider
	// Kind returns the backend kind, e.g. "tart".
	Kind() string
	// Image returns the base image identifier, or "" if the backend has no such
	// concept.
	Image() string
	// RunnerDir returns the directory on the guest where the GitHub Actions
	// runner is installed.
	RunnerDir() string
}
```
Provider is the orchestrator's VM backend abstraction. It is a
vmspool.Provider (construct/list/get/delete the pool's VMs) extended
with the orchestrator specific metadata the rest of the system needs.
Backing pools with a technology other than tart requires only implementing
this interface and adding a corresponding backend section to PoolConfig.
Because each pool selects its own provider, pools using different backends
run simultaneously.


### Type TartConfig
```go
type TartConfig struct {
	tartvm.Config         `yaml:",inline"`
	tartvm.ResourceConfig `yaml:",inline"`
	Image                 string `yaml:"image" doc:"base image to use for cloning VMs in this pool"`
	PullRemoteOnRun       bool   `yaml:"pull_remote_on_run" doc:"pull the image from its registry before creating each VM, so that a moving tag such as :latest is picked up without restarting the orchestrator. Ignored for a local image, which has no registry to pull from."`
	Insecure              bool   `yaml:"pull_insecure" doc:"allow pulling images from insecure registries."`
	PullConcurrency       int    `yaml:"pull_concurrency" doc:"concurrency for a pull of the remote image."`
	RunnerDir             string `yaml:"runner_dir" doc:"directory on the VM in which the runner was installed, specific to each type of image."`
}
```
TartConfig configures the tart VM backend for a pool.





