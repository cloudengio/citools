# Package [github.com/cloudengio/citools/runners/macos/orchestrator/githubwebhook](https://pkg.go.dev/github.com/cloudengio/citools/runners/macos/orchestrator/githubwebhook?tab=doc)

```go
import github.com/cloudengio/citools/runners/macos/orchestrator/githubwebhook
```


## Types
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
func New(relayURL string, handler Handler) *Listener
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
block until the context is cancelled or the listener is stopped. For each
workflow_job event received, the handler function is called in a separate
goroutine.


```go
func (l *Listener) Stop()
```
Stop stops the listener. It is safe to call Stop multiple times.







