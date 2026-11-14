// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

// Package testregistry runs an OCI registry in the test process, so that a
// test needing a real registry needs neither docker nor one on the network. It
// serves the distribution API over plain HTTP on a loopback port, which is
// what a verifier configured for an insecure registry expects, and holds
// everything pushed to it in memory.
package testregistry

import (
	"bytes"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"

	"cloudeng.io/vms/ociimage"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Registry is a registry serving on a loopback port for the duration of a
// test.
type Registry struct {
	server *httptest.Server
	host   string
}

type options struct {
	referrers bool
	logging   bool
}

// Option configures a Registry.
type Option func(*options)

// WithReferrers serves the referrers API, which cosign uses to find the
// signatures of a digest when the registry offers it. It is off by default,
// so that a signature is stored and found under the sha256-<digest> tag
// instead, as it is on a registry:2 that does not implement referrers.
func WithReferrers(enabled bool) Option {
	return func(o *options) { o.referrers = enabled }
}

// WithLogging reports every request the registry serves to the test's log,
// which is off by default: one verification makes a dozen of them, and they
// are only of interest when something is being worked out.
func WithLogging(enabled bool) Option {
	return func(o *options) { o.logging = enabled }
}

// logWriter sends the registry's log to the test's own, where it is shown for
// a failing test and withheld for a passing one.
type logWriter struct{ t testing.TB }

func (w logWriter) Write(p []byte) (int, error) {
	w.t.Logf("registry: %s", bytes.TrimRight(p, "\n"))
	return len(p), nil
}

// New starts a registry and stops it when the test ends.
func New(t testing.TB, opts ...Option) *Registry {
	t.Helper()
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	out := io.Writer(io.Discard)
	if o.logging {
		out = logWriter{t: t}
	}
	server := httptest.NewServer(registry.New(
		registry.WithReferrersSupport(o.referrers),
		registry.Logger(log.New(out, "", 0))))
	t.Cleanup(server.Close)
	return &Registry{
		server: server,
		host:   strings.TrimPrefix(server.URL, "http://"),
	}
}

// Host is the registry's host and port, which is how an image reference names
// it.
func (r *Registry) Host() string { return r.host }

// Close stops the registry before the end of the test, which is how a test
// makes a registry that was reachable unreachable. It can be called more than
// once, as the cleanup New registers does.
func (r *Registry) Close() { r.server.Close() }

// imageLayers and imageBytes are what a pushed image is made of: its content
// does not matter to anything verifying it, only that the layer is random and
// so the image has a digest of its own.
const (
	imageLayers = 1
	imageBytes  = 1024
)

// Push generates an image and pushes it to the registry as path:tag, returning
// the reference it was pushed as, carrying both that tag and the digest it
// resolved to: Reference.WithDigest("") is how a consumer would name it, and
// the digest is what a signature of it covers.
func (r *Registry) Push(t testing.TB, path, tag string) ociimage.Reference {
	t.Helper()
	ref, err := ociimage.Parse(r.host + "/" + path + ":" + tag)
	if err != nil {
		t.Fatalf("testregistry: %v", err)
	}
	image, err := random.Image(imageBytes, imageLayers)
	if err != nil {
		t.Fatalf("testregistry: generating an image: %v", err)
	}
	// go-containerregistry reaches a loopback registry over plain HTTP of its
	// own accord, as it does any registry whose host is local.
	target, err := name.ParseReference(ref.String())
	if err != nil {
		t.Fatalf("testregistry: %v: %v", ref, err)
	}
	if err := remote.Write(target, image); err != nil {
		t.Fatalf("testregistry: pushing %v: %v", ref, err)
	}
	digest, err := image.Digest()
	if err != nil {
		t.Fatalf("testregistry: %v: %v", ref, err)
	}
	return ref.WithDigest(digest.String())
}
