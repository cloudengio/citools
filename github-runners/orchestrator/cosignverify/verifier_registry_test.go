// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package cosignverify_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"cloudeng.io/vms/ociimage"
	"github.com/cloudengio/citools/runners/macos/orchestrator/cosignverify"
	"github.com/cloudengio/citools/runners/macos/orchestrator/internal/testregistry"
)

// The tests in this file run the real cosign against a real registry, whereas
// those in verifier_test.go run a stub cosign, see fakeCosign. The stub is
// what tests how each outcome is reported, since it can produce any of them on
// demand; these tests are what says that cosign still behaves as the stub
// pretends, which is the part that a cosign release can change underneath us.
//
// Nothing here reaches the network: the registry runs in the test process and
// the signature is made with a signing config that names no services, as
// github-runners/cosign.mk does, so no transparency log is involved.

// requireCosign returns the cosign binary, skipping the test if it is not
// installed: it is on the CI images, see github-runners/scripts/cosign.sh, but
// need not be on a developer's machine.
func requireCosign(t *testing.T) string {
	t.Helper()
	binary, err := exec.LookPath(cosignverify.DefaultBinary)
	if err != nil {
		t.Skipf("%v is not installed: %v", cosignverify.DefaultBinary, err)
	}
	return binary
}

// signer signs images with a key pair of its own, as "make sign" does with the
// pair in the keychain.
type signer struct {
	binary  string
	dir     string
	keyFile string
	pubFile string
	config  string // the signing config shared by every signer in dir
}

// newSigner generates a key pair named prefix in dir.
func newSigner(t *testing.T, binary, dir, prefix string) *signer {
	t.Helper()
	s := &signer{
		binary:  binary,
		dir:     dir,
		keyFile: filepath.Join(dir, prefix+".key"),
		pubFile: filepath.Join(dir, prefix+".pub"),
		config:  filepath.Join(dir, "signing-config.json"),
	}
	s.run(t, "generate-key-pair", "--output-key-prefix", filepath.Join(dir, prefix))
	if _, err := os.Stat(s.config); err != nil {
		// A signing config naming no services is what keeps the signature out
		// of the public transparency log, see cosign.mk.
		s.run(t, "signing-config", "create", "--out", s.config)
	}
	return s
}

// run runs cosign, failing the test if it fails. The key is protected by the
// keychain rather than a password where it matters, so there is none here
// either, and cosign has to be told that or it prompts.
func (s *signer) run(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(s.binary, args...)
	cmd.Dir = s.dir
	cmd.Env = append(os.Environ(), "COSIGN_PASSWORD=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cosign %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// publicKey is the PEM of the signer's public key, which is how a policy in a
// configuration file carries it.
func (s *signer) publicKey(t *testing.T) string {
	t.Helper()
	pem, err := os.ReadFile(s.pubFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem)
}

// sign signs the digest ref resolves to, as cosign.mk does: a tag can be moved
// between the push and the signature.
func (s *signer) sign(t *testing.T, ref ociimage.Reference, annotations ...string) {
	t.Helper()
	args := []string{
		"sign", "--yes", "--key", s.keyFile, "--signing-config", s.config,
		"--allow-insecure-registry", "--allow-http-registry",
	}
	for _, a := range annotations {
		args = append(args, "-a", a)
	}
	s.run(t, append(args, ref.WithTag("").String())...)
}

// policyFor is a policy that trusts one key and nothing else.
func policyFor(key string, annotations map[string]string) ociimage.Policy {
	return ociimage.Policy{
		Signers:     []ociimage.Signer{{Key: key}},
		Annotations: annotations,
	}
}

func TestRegistrySigned(t *testing.T) {
	binary := requireCosign(t)
	dir := t.TempDir()
	trusted := newSigner(t, binary, dir, "trusted")
	reg := testregistry.New(t)
	verifier := cosignverify.New(
		cosignverify.WithBinary(binary),
		cosignverify.WithInsecureRegistry(true))

	pushed := reg.Push(t, "signed-ci", "latest")
	trusted.sign(t, pushed)

	// The tag alone is what a consumer names, and the digest is what has to
	// come back pinned to it.
	asked := pushed.WithDigest("")
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"inline PEM", trusted.publicKey(t)},
		{"key file", trusted.pubFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ociimage.Verify(context.Background(), verifier, asked, policyFor(tc.key, nil))
			if err != nil {
				t.Fatalf("%v: %v", asked, err)
			}
			if got, want := res.Reference, pushed; got != want {
				t.Errorf("got %v, want %v", got, want)
			}
			if got, want := len(res.Signatures), 1; got != want {
				t.Fatalf("got %v signatures, want %v", got, want)
			}
			if got, want := res.Signatures[0].Logged, false; got != want {
				t.Errorf("got Logged %v, want %v", got, want)
			}
		})
	}
}

func TestRegistryUnsigned(t *testing.T) {
	binary := requireCosign(t)
	dir := t.TempDir()
	trusted := newSigner(t, binary, dir, "trusted")
	reg := testregistry.New(t)
	verifier := cosignverify.New(
		cosignverify.WithBinary(binary),
		cosignverify.WithInsecureRegistry(true))

	unsigned := reg.Push(t, "unsigned-ci", "latest").WithDigest("")
	_, err := ociimage.Verify(context.Background(), verifier, unsigned, policyFor(trusted.publicKey(t), nil))
	if !errors.Is(err, ociimage.ErrNotVerified) {
		t.Errorf("got %v, want %v", err, ociimage.ErrNotVerified)
	}
	// An image with no signature at all is reported as such, which is what
	// tells a failure to sign apart from a signature that is not acceptable.
	if !errors.Is(err, ociimage.ErrUnsigned) {
		t.Errorf("got %v, want %v", err, ociimage.ErrUnsigned)
	}
}

func TestRegistryUntrustedKey(t *testing.T) {
	binary := requireCosign(t)
	dir := t.TempDir()
	trusted := newSigner(t, binary, dir, "trusted")
	untrusted := newSigner(t, binary, dir, "untrusted")
	reg := testregistry.New(t)
	verifier := cosignverify.New(
		cosignverify.WithBinary(binary),
		cosignverify.WithInsecureRegistry(true))

	pushed := reg.Push(t, "other-key-ci", "latest")
	untrusted.sign(t, pushed)

	asked := pushed.WithDigest("")
	_, err := ociimage.Verify(context.Background(), verifier, asked, policyFor(trusted.publicKey(t), nil))
	if !errors.Is(err, ociimage.ErrNotVerified) {
		t.Errorf("got %v, want %v", err, ociimage.ErrNotVerified)
	}
	// The image is signed, just not by anyone the policy trusts, so the
	// unsigned case has to stay distinct however cosign words its refusal.
	if errors.Is(err, ociimage.ErrUnsigned) {
		t.Errorf("got %v, do not want %v", err, ociimage.ErrUnsigned)
	}
}

func TestRegistryAnnotations(t *testing.T) {
	binary := requireCosign(t)
	dir := t.TempDir()
	trusted := newSigner(t, binary, dir, "trusted")
	reg := testregistry.New(t)
	verifier := cosignverify.New(
		cosignverify.WithBinary(binary),
		cosignverify.WithInsecureRegistry(true))

	pushed := reg.Push(t, "annotated-ci", "latest")
	trusted.sign(t, pushed, "tier=trusted")
	asked := pushed.WithDigest("")
	key := trusted.publicKey(t)

	if _, err := ociimage.Verify(context.Background(), verifier, asked,
		policyFor(key, map[string]string{"tier": "trusted"})); err != nil {
		t.Errorf("the annotation it was signed with: %v", err)
	}
	_, err := ociimage.Verify(context.Background(), verifier, asked,
		policyFor(key, map[string]string{"tier": "other"}))
	if !errors.Is(err, ociimage.ErrNotVerified) {
		t.Errorf("an annotation it was not signed with: got %v, want %v", err, ociimage.ErrNotVerified)
	}
}

func TestRegistryUnreachable(t *testing.T) {
	binary := requireCosign(t)
	dir := t.TempDir()
	trusted := newSigner(t, binary, dir, "trusted")
	reg := testregistry.New(t)
	verifier := cosignverify.New(
		cosignverify.WithBinary(binary),
		cosignverify.WithInsecureRegistry(true))

	pushed := reg.Push(t, "gone-ci", "latest")
	trusted.sign(t, pushed)
	reg.Close()

	_, err := ociimage.Verify(context.Background(), verifier, pushed.WithDigest(""), policyFor(trusted.publicKey(t), nil))
	if err == nil {
		t.Fatal("a registry that cannot be reached was not reported")
	}
	// Nothing is known about the image's signatures, so this is not a verdict
	// on it: a pool must retry rather than conclude that the image is bad.
	if errors.Is(err, ociimage.ErrNotVerified) {
		t.Errorf("got %v, do not want %v", err, ociimage.ErrNotVerified)
	}
}
