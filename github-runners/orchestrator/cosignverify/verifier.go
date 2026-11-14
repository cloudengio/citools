// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

// Package cosignverify verifies the signatures of OCI images by running the
// cosign command, which is how the orchestrator can require a signed image
// without this module depending on sigstore's Go libraries. It implements
// ociimage.Verifier, and verifies what github-runners/cosign.mk signs.
//
// cosign must be installed on the host that verifies, as tart is.
package cosignverify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"cloudeng.io/vms/ociimage"
)

// DefaultBinary is the cosign command run when none is configured, ie. cosign
// as found on PATH.
const DefaultBinary = "cosign"

// cancelGrace is how long a cancelled cosign is given to let go of its output
// before it is let go of instead, see exec.Cmd.WaitDelay.
const cancelGrace = 2 * time.Second

// Verifier verifies the signature of an image by running cosign verify once
// per trusted signer.
type Verifier struct {
	binary   string
	insecure bool
}

var _ ociimage.Verifier = (*Verifier)(nil)

// Option configures a Verifier.
type Option func(*Verifier)

// WithBinary sets the cosign command to run. An empty path keeps
// DefaultBinary, so that it can be passed straight from a configuration file.
func WithBinary(path string) Option {
	return func(v *Verifier) {
		if path != "" {
			v.binary = path
		}
	}
}

// WithInsecureRegistry allows the registry to be reached over plain HTTP, or
// with a TLS certificate that cannot be verified. It is for a private registry
// and goes with the pull_insecure setting of the image being verified: the
// signature is held by the same registry as the image.
func WithInsecureRegistry(insecure bool) Option {
	return func(v *Verifier) { v.insecure = insecure }
}

// New returns a Verifier.
func New(opts ...Option) *Verifier {
	v := &Verifier{binary: DefaultBinary}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// Verify implements ociimage.Verifier. cosign verifies one signer at a time,
// so it is run once per signer and the image is accepted as soon as one of
// them accepts it. The digest cosign reports having verified is what is
// returned, so that the caller uses that image rather than whatever the tag
// names by then.
func (v *Verifier) Verify(ctx context.Context, ref ociimage.Reference, policy ociimage.Policy) (ociimage.Verified, error) {
	if len(policy.Signers) == 0 {
		// ociimage.Verify rejects such a policy before it reaches a verifier;
		// this is here so that nothing is ever accepted by default.
		return ociimage.Verified{}, fmt.Errorf("%w: %v: the policy trusts no signer", ociimage.ErrNotVerified, ref)
	}
	var rejected []error
	for i, signer := range policy.Signers {
		res, err := v.verifyOne(ctx, ref, policy, signer)
		switch {
		case err == nil:
			return res, nil
		case errors.Is(err, ociimage.ErrNotVerified):
			rejected = append(rejected, fmt.Errorf("signers[%v]: %w", i, err))
		default:
			// The verification could not be carried out at all, so nothing is
			// known about the image's signatures and the remaining signers
			// would fail in the same way.
			return ociimage.Verified{}, err
		}
	}
	return ociimage.Verified{}, errors.Join(rejected...)
}

func (v *Verifier) verifyOne(ctx context.Context, ref ociimage.Reference, policy ociimage.Policy, signer ociimage.Signer) (ociimage.Verified, error) {
	args, cleanup, err := v.args(ref, policy, signer)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return ociimage.Verified{}, err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, v.binary, args...) //nolint:gosec // G204: the arguments come from the configuration, not from user input.
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// Cancelling the context kills cosign, but a child of it that holds the
	// output pipes open would otherwise keep Run waiting for as long as that
	// child lives. This closes the pipes shortly after the kill, so a
	// verification that is cancelled actually returns.
	cmd.WaitDelay = cancelGrace
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// The verification was abandoned rather than carried out, so
			// nothing is known about the image's signatures.
			return ociimage.Verified{}, fmt.Errorf("%v verify %v: %w", v.binary, ref, ctxErr)
		}
		return ociimage.Verified{}, classify(v.binary, ref, stderr.String(), err)
	}
	return parse(ref, policy, signer, stdout.Bytes())
}

// args renders the policy and one of its signers as the arguments of cosign
// verify, along with a function to remove the key file if one had to be
// written.
func (v *Verifier) args(ref ociimage.Reference, policy ociimage.Policy, signer ociimage.Signer) ([]string, func(), error) {
	args := []string{"verify", "--output", "json"}
	var cleanup func()
	if signer.Keyless() {
		args = append(args, identityArgs(signer)...)
	} else {
		key, rm, err := keyFile(signer.Key)
		if err != nil {
			return nil, nil, err
		}
		cleanup = rm
		args = append(args, "--key", key)
	}
	if !policy.TransparencyLog {
		// The signature of an image in a private registry is not recorded in
		// a transparency log, so cosign is told not to require one: it fails
		// closed if it is left to look for an entry that was never made.
		args = append(args, "--insecure-ignore-tlog=true")
	}
	// Sorted so that the command is the same for the same policy, which makes
	// it reproducible by hand from a log.
	for _, k := range slices.Sorted(maps.Keys(policy.Annotations)) {
		args = append(args, "-a", k+"="+policy.Annotations[k])
	}
	if v.insecure {
		args = append(args, "--allow-insecure-registry", "--allow-http-registry")
	}
	return append(args, ref.String()), cleanup, nil
}

// identityArgs renders a keyless signer. Signer.Validate guarantees exactly
// one of the exact and the regexp form of each half of the identity.
func identityArgs(s ociimage.Signer) []string {
	var args []string
	if s.Subject != "" {
		args = append(args, "--certificate-identity", s.Subject)
	} else {
		args = append(args, "--certificate-identity-regexp", s.SubjectRegexp)
	}
	if s.Issuer != "" {
		args = append(args, "--certificate-oidc-issuer", s.Issuer)
	} else {
		args = append(args, "--certificate-oidc-issuer-regexp", s.IssuerRegexp)
	}
	return args
}

// inlineKey is what a Signature reports for a key that was configured as the
// PEM itself, since the PEM is of no use as a name for it.
const inlineKey = "inline public key"

// keyFile returns the file to give cosign for a signer's public key. A key is
// either the PEM itself, which a configuration file can carry so that there is
// no key file to distribute to the hosts that verify, or a reference cosign
// resolves for itself: a path, whose leading ~ is expanded here since neither
// cosign nor the configuration parser does it, a URL or a KMS URI.
func keyFile(key string) (string, func(), error) {
	if !strings.HasPrefix(strings.TrimSpace(key), "-----BEGIN") {
		return expandHome(key), nil, nil
	}
	f, err := os.CreateTemp("", "cosign-*.pub")
	if err != nil {
		return "", nil, fmt.Errorf("creating a file for the public key: %w", err)
	}
	name := f.Name()
	remove := func() { os.Remove(name) } //nolint:errcheck // the key is public and in the temp directory
	if _, err := f.WriteString(strings.TrimSpace(key) + "\n"); err != nil {
		f.Close() //nolint:errcheck,gosec // the write already failed
		remove()
		return "", nil, fmt.Errorf("writing the public key to %v: %w", name, err)
	}
	if err := f.Close(); err != nil {
		remove()
		return "", nil, fmt.Errorf("writing the public key to %v: %w", name, err)
	}
	return name, remove, nil
}

// expandHome expands a leading ~ in a path, and leaves anything else, such as
// a URL or a KMS URI, as it is.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// unreachable are what cosign says when it never got a verdict on the image:
// it could not reach the registry, or was not allowed to read it. This set is
// small and stable, being the vocabulary of Go's net, tls and x509 packages
// and of a registry's own refusals, which is why it is the set that is
// matched rather than the other one, see classify.
var unreachable = []string{
	"dial tcp", "no such host", "connection refused", "connection reset",
	"i/o timeout", "context deadline exceeded", "client.timeout",
	"tls:", "x509:", "server gave http response to https client",
	"unauthorized", "authentication required", "denied:",
	"internal server error", "service unavailable", "too many requests",
}

// unsigned are what cosign says for an image that carries no signature at
// all, as opposed to one whose signatures are not acceptable.
var unsigned = []string{"no signatures found", "no signatures associated"}

// classify turns a failed cosign run into the error the ociimage.Verifier
// contract asks for: ErrNotVerified when the image does not satisfy the
// policy, and any other error when cosign could not tell, such as a registry
// it cannot reach. Only cosign's message distinguishes the two, so the text is
// matched.
//
// What is matched is the failure to reach the registry, and everything else is
// taken to be a verdict on the image. That way round because cosign's
// vocabulary for refusing an image is large and changes between releases,
// whereas the transport errors beneath it do not: a release that rewords a
// refusal then still leaves a rejected signature reported as ErrNotVerified,
// so a policy that trusts several signers goes on to try the next one instead
// of giving up. The image is not to be used either way.
func classify(binary string, ref ociimage.Reference, stderr string, runErr error) error {
	// A cosign that could not be started, or that did not run to a decision,
	// has said nothing about the image.
	if _, ok := errors.AsType[*exec.ExitError](runErr); !ok {
		return fmt.Errorf("%v verify %v: %w", binary, ref, runErr)
	}
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = runErr.Error()
	}
	switch {
	case mentions(msg, unsigned...):
		return fmt.Errorf("%w: %w: %v: %v", ociimage.ErrNotVerified, ociimage.ErrUnsigned, ref, msg)
	case mentions(msg, unreachable...):
		return fmt.Errorf("%v verify %v: %v: %w", binary, ref, msg, runErr)
	}
	// An image that does not satisfy the policy, including one that is not in
	// the registry at all: nothing is there to have been signed, but calling
	// that unsigned would be misleading.
	return fmt.Errorf("%w: %v: %v", ociimage.ErrNotVerified, ref, msg)
}

func mentions(msg string, any ...string) bool {
	msg = strings.ToLower(msg)
	for _, s := range any {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// payload is the part of cosign's output that is needed: the signing payload
// of each signature it accepted.
type payload struct {
	Critical struct {
		Image struct {
			Digest string `json:"docker-manifest-digest"`
		} `json:"image"`
	} `json:"critical"`
	Optional map[string]any `json:"optional"`
}

// parse reads what cosign verified: the payload of each accepted signature,
// all of which name the one digest that the image's tag resolved to.
func parse(ref ociimage.Reference, policy ociimage.Policy, signer ociimage.Signer, out []byte) (ociimage.Verified, error) {
	var payloads []payload
	if err := json.Unmarshal(out, &payloads); err != nil {
		return ociimage.Verified{}, fmt.Errorf("reading the output of cosign verify %v: %w", ref, err)
	}
	if len(payloads) == 0 {
		return ociimage.Verified{}, fmt.Errorf("%w: %w: %v: cosign accepted it but reported no signature", ociimage.ErrNotVerified, ociimage.ErrUnsigned, ref)
	}
	var verified ociimage.Verified
	digest := ""
	for _, p := range payloads {
		switch d := p.Critical.Image.Digest; {
		case d == "":
			return ociimage.Verified{}, fmt.Errorf("%w: %v: cosign reported a signature with no digest", ociimage.ErrNotVerified, ref)
		case digest == "":
			digest = d
		case d != digest:
			return ociimage.Verified{}, fmt.Errorf("%w: %v: cosign verified two digests, %v and %v", ociimage.ErrNotVerified, ref, digest, d)
		}
		verified.Signatures = append(verified.Signatures, signature(policy, signer, p))
	}
	verified.Reference = ref.WithDigest(digest)
	return verified, nil
}

// signature describes a signature cosign accepted. The identity of a keyless
// one is taken from the payload when it names one, and otherwise from the
// signer that cosign matched it against.
func signature(policy ociimage.Policy, signer ociimage.Signer, p payload) ociimage.Signature {
	sig := ociimage.Signature{
		Issuer:  signer.Issuer,
		Subject: signer.Subject,
		Logged:  policy.TransparencyLog,
	}
	if !signer.Keyless() {
		sig.Key = signer.Key
		if strings.HasPrefix(strings.TrimSpace(signer.Key), "-----BEGIN") {
			sig.Key = inlineKey
		}
	}
	for k, val := range p.Optional {
		if k == "Bundle" {
			// cosign reports the transparency log entry here, so the
			// signature is logged whether or not the policy required it.
			sig.Logged = true
			continue
		}
		s, ok := val.(string)
		if !ok {
			continue
		}
		switch k {
		case "Subject":
			sig.Subject = s
		case "Issuer":
			sig.Issuer = s
		default:
			if sig.Annotations == nil {
				sig.Annotations = map[string]string{}
			}
			sig.Annotations[k] = s
		}
	}
	return sig
}
