# Package [github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient](https://pkg.go.dev/github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient?tab=doc)

```go
import github.com/cloudengio/citools/runners/macos/orchestrator/vmsclient
```

Package vmsclient runs the VM pools that the orchestrator runs its jobs on,
on top of the pool implementation in cloudeng.io/vms/vmspool. That package
creates, fills and drains a single pool of VMs; this one adds what the
orchestrator needs around it:

  - the backends it supports, tart and the mock, as
    cloudeng.io/vms/vmspool.Provider implementations, and the configuration
    file syntax that selects and configures one of them for each pool,
    see PoolConfig and BackendConfig,
  - the configuration of a whole set of pools, so that pools backed
    by different technologies are configured and created together,
    see NewPools. Holding them afterwards, addressing them by name,
    acquiring and refreshing, reporting changes and closing is
    cloudeng.io/vms/vmspool.Set, which Pools embeds,
  - the status snapshots the web UI needs, see PoolSnapshot, whose lifecycle
    counters are a basepool.StatusTracker's, one of which the whole set
    shares, and the capacity the webhook listener's connection is gated on,
    see Pools.HasCapacity,
  - the creation and lifetime of a completion queue, which parks the VMs
    of finished jobs for a while so that they can be inspected before
    they are deleted, see WithCompletionOutcomes. The queue itself is
    basepool.CompletionQueue, a component in its own right; a set creates
    one and hands it to its pools, which hand it to the VMs they create and
    count the ones it is holding. Releasing it, after the pools that park
    VMs in it have stopped, is basepool.Set.Close's doing,
  - the directory the GitHub Actions runner is installed in on the guest,
    which is a property of the image rather than anything a pool needs to
    know about, see PoolConfig.RunnerDir.

Nothing but the backends depends on a particular VM technology. Supporting
another one means implementing basepool.Provider, the BackendConfig that
builds it and a section for it in PoolConfig, and nothing else; the pools,
the status reporting and the completion queue are unchanged by it.

Pools are created with NewPools, which is the only way to create one: the
pools of a set share the accounting of their events — every event names the
pool that emitted it, see basepool.Event.Pool, so one basepool.StatusTracker
counts all of them and its change signal covers all of them — and the
completion queue their finished VMs are parked in. A single pool is a set of
one, and pays for nothing more.

## Constants
### DefaultEventBuffer
```go
const DefaultEventBuffer = basepool.DefaultStatusBuffer
```
DefaultEventBuffer is the number of lifecycle events buffered for a pool
whilst they are waiting to be counted, see WithEventBuffer. It is the
tracker's own default, since the counting is the tracker's.

### DefaultMockImage
```go
const DefaultMockImage = "mock"
```
DefaultMockImage is reported by a mock pool that configures no image,
so that status output has something to show.

### MockKind
```go
const MockKind = vmstestutil.MockProviderName
```
MockKind names the mock VM technology, as both the configuration that
selects it and the backend that implements it report it. It is vmstestutil's
own name for itself, so that the two cannot drift apart.

### TartKind
```go
const TartKind = tartvm.ProviderName
```
TartKind names the tart VM technology, as both the configuration that
selects it and the backend that implements it report it. It is tartvm's own
name for itself, so that the two cannot drift apart.

### VMSPrefix
```go
const VMSPrefix = "ghr-orchestrator-"
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
ErrNoBackend = errors.New("no VM backend configured")

```
ErrNoBackend is returned for a pool configuration that selects no backend,
and so has nothing to create VMs with.

### ErrNocachingProvider
```go
ErrNocachingProvider = errors.New("the pool's backend is not a vmspool.CachingProvider")

```
ErrNocachingProvider is returned by GetSnapshot for a pool whose backend is
not a CachingProvider.



## Functions
### Func DeletePoolVMs
```go
func DeletePoolVMs(ctx context.Context, cfgs map[string]PoolConfig, stopTimeout time.Duration) ([]string, error)
```
DeletePoolVMs deletes the VMs of every configured pool, across all of their
backends, and returns the names of those deleted, see basepool.DeleteVMs.
Like ListPoolVMs it needs no running pool, which is what lets it clean up
after a run that exited without doing so itself.

### Func ListPoolVMs
```go
func ListPoolVMs(ctx context.Context, cfgs map[string]PoolConfig) ([]basepool.VMInfo, error)
```
ListPoolVMs returns the VMs that currently exist for every configured pool,
across all of their backends, see basepool.ListVMs. It needs no running
pool, so it reports the VMs left behind by a previous run as readily as the
ones in use by this one.



## Types
### Type BackendConfig
```go
type BackendConfig interface {
	// Validate reports whether the configuration is usable, so that a mistake
	// in a configuration file is caught when it is read rather than when the
	// pool first tries to create a VM.
	Validate() error
	// Backend returns what the named pool's backend is created by: a
	// basepool.ProviderNew, which names the VM technology and builds the
	// provider that implements it. Calling it creates nothing — the backend
	// itself is built by basepool.ProviderNew.New, whose context carries the
	// logger the backend captures for its lifetime, see ctxlog.Logger — so the
	// kind of a configured pool can be reported without a backend being built
	// for it.
	//
	// The pool's name is bound here rather than passed to New, which builds a
	// provider from nothing but a context: a configuration does not carry the
	// name of the pool it configures, that being the key it is stored under,
	// and a backend needs the name because by convention it includes it in the
	// names of the VMs it creates, so that the VMs of one pool can be told
	// from another's and those left behind by a previous run can be found
	// again.
	Backend(pool string) basepool.ProviderNew
}
```
BackendConfig is the configuration of a pool's backend: it validates itself
and names what builds the backend it describes. TartConfig and MockConfig
are the two, each a section of a PoolConfig.

A backend is a basepool.Provider, the VM technology that creates, lists,
inspects and deletes the pool's VMs. It is all a pool knows of the
technology underneath it, so supporting a new one means implementing
that interface, the BackendConfig that builds it and a section for it in
PoolConfig, and changing nothing else. Its Name is the VM technology, eg.
"tart", and its Details report the base image the pool's VMs are created
from; PoolSnapshot.Kind and PoolSnapshot.Image are those two, named as the
web UI reports them. Its DefaultResources state what its VMs are created
with, which Pools.Status uses to fill in the figures a VM does not report
for itself.


### Type MockConfig
```go
type MockConfig struct {
	// Image is reported in status output in place of a real base image, so
	// that a mock pool is identifiable at a glance. Nothing is created from
	// it, but it must still be a reference, see ociimage.Parse.
	Image string `yaml:"image" doc:"image reference reported in place of a base image for this pool; nothing is created from it, but it must still parse as one. Defaults to 'mock'"`
	// Suspendable selects whether the mock VMs support suspend and resume,
	// which determines whether a pool configured with the suspended staging
	// behaviour suspends its VMs or falls back to stopping them.
	Suspendable bool `yaml:"suspendable" doc:"whether the mock VMs support suspend/resume; when false a suspended pool falls back to stopping its VMs"`
	// RunnerDir mirrors the tart backend's setting so that the runner
	// installation commands are built the same way.
	RunnerDir string `yaml:"runner_dir" doc:"directory on the guest in which the runner is installed; only recorded, never used"`
}
```
MockConfig configures the mock VM backend. A pool backed by it behaves
exactly as a real one — it creates, stages, hands out and deletes VMs,
and reports the same events — but the VMs are in-process fakes that run
no commands: anything that would be run inside one is recorded rather than
executed, so a mock VM reports success for anything.

It exists so that the orchestrator can be exercised end to end, in tests and
by hand, on a machine with no VM technology installed at all. It is also the
backend this package's own tests use.

### Methods

```go
func (cfg MockConfig) Backend(pool string) basepool.ProviderNew
```
Backend implements BackendConfig.


```go
func (cfg MockConfig) Validate() error
```
Validate implements BackendConfig. A mock pool needs nothing of its own:
its VMs are created in process, from no image at all. The image it reports
in their place must still be a reference, so that a configuration written
against a mock pool and then pointed at a real backend does not fail only
then.




### Type Option
```go
type Option func(*options)
```
Option configures a pool, or every pool of a set, at creation.

### Functions

```go
func WithBaseOptions(opts ...basepool.Option) Option
```
WithBaseOptions passes options through to the underlying
cloudeng.io/vms/vmspool.Pool, for the settings that PoolConfig does not
carry: where a VM's output is written, for example. Note that the status
channel is not one of them. It is set here, since the pool's event stream is
what the counters in a PoolSnapshot are accumulated from, and an option that
replaced it would silence them.


```go
func WithCompletionOutcomes(outcomes ...basepool.CompletionOutcomeOption) Option
```
WithCompletionOutcomes gives the set a completion queue, which parks the
VMs of finished work rather than deleting them at once, and configures
the outcomes that work is filed under: each has a capacity and a retention
period of its own, see basepool.WithCompletionOutcome. A set configured with
no outcomes has no queue at all, which is an application that deletes each
VM as it finishes with it.

The queue is created with the set, shared by every pool in it and closed
when the set is, see Pools.CompletionQueue: it is the set that owns it,
because the VMs of several pools are parked in the one queue and releasing
them is the last thing to happen. A caller fills and drains it, and must not
close it.

The pools themselves neither fill the queue nor drain it; that is for the
application, which knows what finishing means. What a pool does with it is
hand it to every VM it creates, see basepool.VM.CompletionQueue, so that
work holding a VM can park it without being given the queue as well, and
report the VMs of its own that it is holding, see PoolSnapshot.Completing.


```go
func WithEventBuffer(size int) Option
```
WithEventBuffer sets how many of a pool's lifecycle events are buffered
whilst they are waiting to be counted; 0 or less is DefaultEventBuffer.
Events are dropped rather than queued once the buffer is full, so this
bounds how bursty a pool can be before its counters start to undercount;
it has no effect on the pool itself.




### Type PoolConfig
```go
type PoolConfig struct {
	basepool.Config `yaml:",inline"`
	Tart            *TartConfig `yaml:"tart_config" doc:"configure and select the tart VM backend for this pool"`
	Mock            *MockConfig `yaml:"mock_config" doc:"configure and select the mock VM backend for this pool, for exercising the orchestrator without any VM technology installed"`
	// Backend selects the backend directly, for a caller that assembles a
	// PoolConfig rather than reading one: it comes from no configuration file,
	// and counts as the one backend a pool may have.
	Backend BackendConfig `yaml:"-"`
}
```
PoolConfig configures a single VM pool: the generic pool settings of
cloudeng.io/vms/vmspool.Config, which govern how large the pool is and how
its VMs are created, staged and reclaimed, plus exactly one backend section
that selects and configures the VM technology (eg. tart) that backs it.
Different pools may use different backends.

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
that its required settings are present, so that a mistake in a configuration
file is caught when it is read rather than when the pool first tries to
create a VM.




### Type PoolSnapshot
```go
type PoolSnapshot struct {
	Name  string
	Kind  string
	Image string
	// Size is the number of VMs the pool is configured to hold.
	Size int
	// Available, Acquired and Pending are the pool's own counts of VMs that
	// can be acquired now, are held by a caller, and are being created.
	Available int
	Acquired  int
	Pending   int
	// Completing is the number of this pool's VMs parked in the completion
	// queue, if one is attached; they have been acquired and finished with
	// but are kept for a while before being deleted.
	Completing int
	// VMs is the ground truth from the backend: the VMs that exist, whatever
	// the pool believes about them.
	VMs []basepool.VMDetail
	// Counters counts the pool's lifecycle events by kind, see
	// cloudeng.io/vms/vmspool.EventKind.
	Counters map[string]int
	// Updated is the time of the most recent event counted.
	Updated time.Time
}
```
PoolSnapshot is a point-in-time view of a single pool: which backend it
uses, the VMs currently backing it, as reported by that backend, and the
aggregate lifecycle counters accumulated from the pool's event stream.

### Functions

```go
func GetSnapshot(ctx context.Context, pool *basepool.Pool) (PoolSnapshot, []string, error)
```
GetSnapshot returns a point-in-time view of a single pool. That pool must
have been created with a CachingProvider.




### Type Pools
```go
type Pools struct {
	*basepool.Set
}
```
Pools is a set of named pools, each with a backend of its own, so that pools
backed by different VM technologies run side by side. It is a basepool.Set
plus what this package adds: the configured backends the pools are created
from, see BackendConfig, and the status and capacity reporting the web UI
and the webhook listener need, see PoolSnapshot and Pools.HasCapacity.

The embedded set owns the pools, the completion queue and the status
tracker, and supplies everything that is not specific to the orchestrator:
addressing a pool by name, acquiring and refreshing, the change signal and
closing. Only Close is overridden, to log it.

### Functions

```go
func NewPools(ctx context.Context, cfgs map[string]PoolConfig, createFile func(string) io.Writer, opts ...Option) (*Pools, error)
```
NewPools creates and starts every configured pool and returns them as a set,
which is the only way a pool is created: a pool reports to the set's
accounting, so a single pool is a set of one. The pools are created
concurrently, since filling one means creating VMs and that is slow.
createFile supplies the writer that a VM's output is recorded to, given the
VM's ID.

It is all or nothing. If any pool cannot be created or started, those that
were are closed again and the error is returned: a half-started set would
otherwise leave VMs running with nothing left to reclaim them.



### Methods

```go
func (p *Pools) Close(ctx context.Context) error
```
Close closes every pool, then the completion queue and the accounting they
share, see basepool.Set.Close. It is idempotent.


```go
func (p *Pools) ClosePool(ctx context.Context, name string) error
```
ClosePool closes the named pool, leaving the rest of the set running.
The completion queue is not closed, since the VMs it holds may have come
from any pool; this pool's are released when the queue expires them or the
set is closed, see Pools.Close.


```go
func (p *Pools) HasCapacity() bool
```
HasCapacity reports whether any pool can supply a VM now or shortly, ie.
whether there is any point in taking on more work. A set with no pools in
it, or a closed one, has no capacity: there is nothing for any work to run
on.

A VM that is already held by a caller is not capacity for another one:
a pool is of a fixed size. A VM being created is, even though Acquire has
to wait for it: a pool starts replacing a VM as soon as the caller holding
it releases it, so treating a replenishing pool as full would mean refusing
work for as long as it takes to create a VM, repeatedly, whilst Acquire
would have waited for it anyway.

The pools are not interchangeable, since work names the pool it must run in,
so capacity in one does not mean that a particular piece of work can be run.
It is a measure of whether there is any work that can be taken on at all.


```go
func (p *Pools) PoolStatus(ctx context.Context, name string) (PoolSnapshot, error)
```
PoolStatus returns a snapshot of the named pool. It is the set that reports
a pool because the lifecycle counters in a snapshot are the set's tracker's,
see NewPools.


```go
func (p *Pools) Status(ctx context.Context) ([]PoolSnapshot, error)
```
Status returns a snapshot of every pool, sorted by name, accumulating the
backends' errors so that one failing backend does not hide what the others
report. A closed set reports its pools as a running one does.




### Type TartConfig
```go
type TartConfig struct {
	tartvm.Config        `yaml:",inline"`
	ociimage.Source      `yaml:",inline"`
	basepool.VMResources `yaml:",inline"`
	RunnerDir            string `yaml:"runner_dir" doc:"directory on the VM in which the runner was installed, specific to each type of image."`
}
```
TartConfig configures the tart VM backend for a pool: how tart itself is
run, which image its VMs are cloned from and how that image is obtained,
the resources the VMs are given, and where the runner lives inside them.

### Methods

```go
func (cfg TartConfig) Backend(pool string) basepool.ProviderNew
```
Backend implements BackendConfig.


```go
func (cfg TartConfig) Validate() error
```
Validate implements BackendConfig. An image and the runner directory
within it are both required: tart has nothing to clone without the first,
and the orchestrator has nowhere to install the runner without the second.
The image must also be a reference that can be parsed, so that one that
cannot is reported when the configuration file is read rather than when the
first VM fails to be cloned.







