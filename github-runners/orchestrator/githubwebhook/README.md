# Package [github.com/cloudengio/citools/runners/macos/orchestrator/githubwebhook](https://pkg.go.dev/github.com/cloudengio/citools/runners/macos/orchestrator/githubwebhook?tab=doc)

```go
import github.com/cloudengio/citools/runners/macos/orchestrator/githubwebhook
```


## Types
### Type Capacity
```go
type Capacity interface {
	// HasCapacity reports whether work can be taken on now. It is called
	// often and from the listener's own goroutines, so it must be cheap and
	// safe for concurrent use.
	HasCapacity() bool
	// Changed returns a channel that is signalled when the answer HasCapacity
	// gives may have changed. The channel is read but never closed, signals on
	// it may be coalesced, and it may be nil for an implementation that has no
	// such signal: the listener also re-reads the capacity periodically, so a
	// signal makes it react promptly rather than at all.
	Changed() <-chan struct{}
}
```
Capacity reports whether the orchestrator can take on new work. A listener
configured with one, see WithCapacity, connects to the relay only whilst
it can, which leaves the events for the time being queued at the relay,
or to be read by another orchestrator reading the same relay.


### Type Handler
```go
type Handler func(context.Context, *gogithub.WorkflowJobEvent) error
```
Handler processes a single workflow_job webhook event. Any error it returns
is logged; because handlers run in their own goroutine there is no caller to
return it to.


### Type Listener
```go
type Listener struct {
	// contains filtered or unexported fields
}
```

### Functions

```go
func New(relayURL string, handler Handler, opts ...Option) *Listener
```



### Methods

```go
func (l *Listener) DoneCh() <-chan struct{}
```
DoneCh returns a channel that is closed when the listener is stopped.
It can be used to wait for the listener to stop.


```go
func (l *Listener) Listen(ctx context.Context, opts []operations.Option) error
```
Listen starts listening for workflow_job events from the relay. It will
block until the context is cancelled, the listener is stopped, or the relay
closes cleanly. For each workflow_job event received, the handler function
is called in a separate goroutine.

A listener configured with a Capacity, see WithCapacity, reads from the
relay only whilst there is work it can take on: it drops its connection when
there is not, and re-establishes it when there is again.


```go
func (l *Listener) Stop()
```
Stop stops the listener. It is safe to call Stop multiple times.




### Type Option
```go
type Option func(*Listener)
```
Option configures a Listener.

### Functions

```go
func WithCapacity(c Capacity) Option
```
WithCapacity configures the listener to connect to the relay only whilst c
reports that work can be taken on. Without it the listener stays connected
regardless of whether anything can be done with the events it reads.

The relay queues what it receives whilst nothing is reading it, so the
events arrive once there is capacity again. They are not kept indefinitely:
a relay drops the oldest delivery when its queue is full and, if it is
configured to, expires them, so a listener that stays away for long enough
can still miss events.


```go
func WithStreaming(streaming bool) Option
```
WithStreaming configures whether the listener uses the streaming webhook API
(stream=1) instead of polling hanging GET requests.







