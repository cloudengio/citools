# Package [github.com/cloudengio/citools/runners/macos/orchestrator/cosignverify](https://pkg.go.dev/github.com/cloudengio/citools/runners/macos/orchestrator/cosignverify?tab=doc)

```go
import github.com/cloudengio/citools/runners/macos/orchestrator/cosignverify
```

Package cosignverify verifies the signatures of OCI images by running the
cosign command, which is how the orchestrator can require a signed image
without this module depending on sigstore's Go libraries. It implements
ociimage.Verifier, and verifies what github-runners/cosign.mk signs.

cosign must be installed on the host that verifies, as tart is.

## Constants
### DefaultBinary
```go
const DefaultBinary = "cosign"
```
DefaultBinary is the cosign command run when none is configured, ie.
cosign as found on PATH.



## Types
### Type Option
```go
type Option func(*Verifier)
```
Option configures a Verifier.

### Functions

```go
func WithBinary(path string) Option
```
WithBinary sets the cosign command to run. An empty path keeps
DefaultBinary, so that it can be passed straight from a configuration file.


```go
func WithInsecureRegistry(insecure bool) Option
```
WithInsecureRegistry allows the registry to be reached over plain HTTP, or
with a TLS certificate that cannot be verified. It is for a private registry
and goes with the pull_insecure setting of the image being verified:
the signature is held by the same registry as the image.




### Type Verifier
```go
type Verifier struct {
	// contains filtered or unexported fields
}
```
Verifier verifies the signature of an image by running cosign verify once
per trusted signer.

### Functions

```go
func New(opts ...Option) *Verifier
```
New returns a Verifier.



### Methods

```go
func (v *Verifier) Verify(ctx context.Context, ref ociimage.Reference, policy ociimage.Policy) (ociimage.Verified, error)
```
Verify implements ociimage.Verifier. cosign verifies one signer at a time,
so it is run once per signer and the image is accepted as soon as one
of them accepts it. The digest cosign reports having verified is what is
returned, so that the caller uses that image rather than whatever the tag
names by then.







