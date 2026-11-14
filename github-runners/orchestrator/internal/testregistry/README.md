# Package [github.com/cloudengio/citools/runners/macos/orchestrator/internal/testregistry](https://pkg.go.dev/github.com/cloudengio/citools/runners/macos/orchestrator/internal/testregistry?tab=doc)

```go
import github.com/cloudengio/citools/runners/macos/orchestrator/internal/testregistry
```

Package testregistry runs an OCI registry in the test process, so that a
test needing a real registry needs neither docker nor one on the network.
It serves the distribution API over plain HTTP on a loopback port,
which is what a verifier configured for an insecure registry expects,
and holds everything pushed to it in memory.

## Types
### Type Option
```go
type Option func(*options)
```
Option configures a Registry.

### Functions

```go
func WithLogging(enabled bool) Option
```
WithLogging reports every request the registry serves to the test's log,
which is off by default: one verification makes a dozen of them, and they
are only of interest when something is being worked out.


```go
func WithReferrers(enabled bool) Option
```
WithReferrers serves the referrers API, which cosign uses to find the
signatures of a digest when the registry offers it. It is off by default, so
that a signature is stored and found under the sha256-<digest> tag instead,
as it is on a registry:2 that does not implement referrers.




### Type Registry
```go
type Registry struct {
	// contains filtered or unexported fields
}
```
Registry is a registry serving on a loopback port for the duration of a
test.

### Functions

```go
func New(t testing.TB, opts ...Option) *Registry
```
New starts a registry and stops it when the test ends.



### Methods

```go
func (r *Registry) Close()
```
Close stops the registry before the end of the test, which is how a test
makes a registry that was reachable unreachable. It can be called more than
once, as the cleanup New registers does.


```go
func (r *Registry) Host() string
```
Host is the registry's host and port, which is how an image reference names
it.


```go
func (r *Registry) Push(t testing.TB, path, tag string) ociimage.Reference
```
Push generates an image and pushes it to the registry as path:tag, returning
the reference it was pushed as, carrying both that tag and the digest it
resolved to: Reference.WithDigest("") is how a consumer would name it,
and the digest is what a signature of it covers.







