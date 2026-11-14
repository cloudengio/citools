// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package cosignverify_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloudeng.io/vms/ociimage"
	"github.com/cloudengio/citools/runners/macos/orchestrator/cosignverify"
)

const (
	testDigest = "sha256:1b7c4f2e9d0a5c6e8f3b2a1d4c5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e"
	testImage  = "mm-1:5001/macos-ci:latest"
)

// payloadJSON is the output of "cosign verify --output json": the signing
// payload of each accepted signature.
func payloadJSON(digest string, optional string) string {
	if optional == "" {
		optional = "{}"
	}
	return `[{"critical":{"identity":{"docker-reference":"mm-1:5001/macos-ci"},` +
		`"image":{"docker-manifest-digest":"` + digest + `"},` +
		`"type":"cosign container image signature"},"optional":` + optional + `}]`
}

// fakeCosign puts a stub cosign on PATH that writes stdout and stderr and
// exits with code. The returned functions report the arguments of each
// invocation, and the contents of the file that was passed to --key.
func fakeCosign(t *testing.T, stdout, stderr string, code int) (invocations func() []string, keyFile func() string) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	key := filepath.Join(dir, "key")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + strconv.Quote(calls) + "\n" +
		"prev=\"\"\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"--key\" ] && [ -f \"$a\" ]; then cp \"$a\" " + strconv.Quote(key) + "; fi\n" +
		"  prev=\"$a\"\n" +
		"done\n" +
		"printf '%s' " + strconv.Quote(stdout) + "\n" +
		"printf '%s' " + strconv.Quote(stderr) + " >&2\n" +
		"exit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "cosign"), []byte(script), 0700); err != nil { //nolint:gosec // test stub must be executable
		t.Fatalf("writing the cosign stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
			buf, err := os.ReadFile(calls)
			if err != nil {
				return nil // never invoked
			}
			return strings.Split(strings.TrimSpace(string(buf)), "\n")
		}, func() string {
			buf, err := os.ReadFile(key)
			if err != nil {
				return ""
			}
			return string(buf)
		}
}

func testRef(t *testing.T) ociimage.Reference {
	t.Helper()
	ref, err := ociimage.Parse(testImage)
	if err != nil {
		t.Fatalf("parsing %v: %v", testImage, err)
	}
	return ref
}

// TestVerifyKey covers the common case: a key signature with no transparency
// log entry, which is what an image in a private registry has. The digest
// cosign reports is what the caller is given, so that it uses that image and
// not the tag.
func TestVerifyKey(t *testing.T) {
	invocations, _ := fakeCosign(t, payloadJSON(testDigest, ""), "", 0)
	policy := ociimage.Policy{Signers: []ociimage.Signer{{Key: "/keys/ci.pub"}}}

	res, err := cosignverify.New().Verify(t.Context(), testRef(t), policy)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got, want := res.Reference.String(), "mm-1:5001/macos-ci:latest@"+testDigest; got != want {
		t.Errorf("reference: got %v, want %v", got, want)
	}
	if got, want := len(res.Signatures), 1; got != want {
		t.Fatalf("signatures: got %v, want %v", got, want)
	}
	if got, want := res.Signatures[0].Key, "/keys/ci.pub"; got != want {
		t.Errorf("key: got %v, want %v", got, want)
	}
	if res.Signatures[0].Logged {
		t.Error("the signature is reported as logged, but no log was required or named")
	}
	want := "verify --output json --key /keys/ci.pub --insecure-ignore-tlog=true " + testImage
	if got := invocations(); len(got) != 1 || got[0] != want {
		t.Errorf("cosign invocations: got %v, want [%v]", got, want)
	}
}

// TestVerifyPolicyFlags covers what the rest of a policy becomes on the
// command line.
func TestVerifyPolicyFlags(t *testing.T) {
	for _, tc := range []struct {
		name     string
		policy   ociimage.Policy
		insecure bool
		want     string
	}{
		{"insecure registry",
			ociimage.Policy{Signers: []ociimage.Signer{{Key: "k.pub"}}}, true,
			"verify --output json --key k.pub --insecure-ignore-tlog=true --allow-insecure-registry --allow-http-registry " + testImage},
		// A policy that requires a log entry lets cosign check for one.
		{"transparency log",
			ociimage.Policy{Signers: []ociimage.Signer{{Key: "k.pub"}}, TransparencyLog: true}, false,
			"verify --output json --key k.pub " + testImage},
		// The annotations are cosign's to enforce, and are sorted so that the
		// same policy always produces the same command.
		{"annotations",
			ociimage.Policy{Signers: []ociimage.Signer{{Key: "k.pub"}},
				Annotations: map[string]string{"tier": "trusted", "built_by": "ci"}}, false,
			"verify --output json --key k.pub --insecure-ignore-tlog=true -a built_by=ci -a tier=trusted " + testImage},
		{"keyless identity",
			ociimage.Policy{Signers: []ociimage.Signer{{
				Subject: "cnicolaou@cloudeng.io", Issuer: "https://oauth2.sigstore.dev/auth"}}}, false,
			"verify --output json --certificate-identity cnicolaou@cloudeng.io " +
				"--certificate-oidc-issuer https://oauth2.sigstore.dev/auth --insecure-ignore-tlog=true " + testImage},
		{"keyless identity by pattern",
			ociimage.Policy{Signers: []ociimage.Signer{{
				SubjectRegexp: ".*@cloudeng[.]io", IssuerRegexp: "https://.*"}}}, false,
			"verify --output json --certificate-identity-regexp .*@cloudeng[.]io " +
				"--certificate-oidc-issuer-regexp https://.* --insecure-ignore-tlog=true " + testImage},
	} {
		invocations, _ := fakeCosign(t, payloadJSON(testDigest, ""), "", 0)
		v := cosignverify.New(cosignverify.WithInsecureRegistry(tc.insecure))
		if _, err := v.Verify(t.Context(), testRef(t), tc.policy); err != nil {
			t.Errorf("%v: Verify: %v", tc.name, err)
			continue
		}
		if got := invocations(); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%v: got %v, want [%v]", tc.name, got, tc.want)
		}
	}
}

// TestVerifyInlineKey covers a public key carried in the configuration itself
// rather than in a file on each host that verifies: it reaches cosign as a
// file, which is the only form it takes, and is not left behind.
func TestVerifyInlineKey(t *testing.T) {
	const pem = "-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEtest\n-----END PUBLIC KEY-----"
	invocations, keyFile := fakeCosign(t, payloadJSON(testDigest, ""), "", 0)
	policy := ociimage.Policy{Signers: []ociimage.Signer{{Key: pem + "\n"}}}

	res, err := cosignverify.New().Verify(t.Context(), testRef(t), policy)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got, want := keyFile(), pem+"\n"; got != want {
		t.Errorf("the key cosign was given: got %q, want %q", got, want)
	}
	// The PEM is of no use as the name of the key that verified.
	if got, want := res.Signatures[0].Key, "inline public key"; got != want {
		t.Errorf("key: got %q, want %q", got, want)
	}
	// The file is removed once cosign has read it.
	got := invocations()
	if len(got) != 1 {
		t.Fatalf("cosign invocations: %v", got)
	}
	fields := strings.Fields(got[0])
	i := slices.Index(fields, "--key")
	if i < 0 {
		t.Fatalf("no --key in %v", got[0])
	}
	if _, err := os.Stat(fields[i+1]); !os.IsNotExist(err) {
		t.Errorf("%v was left behind: %v", fields[i+1], err)
	}
}

// TestVerifyRejected covers what cosign says when it will not accept an image,
// and the error each case has to produce: an unsigned image is distinguished
// from a signature that does not satisfy the policy, and neither is confused
// with a registry that cannot be reached.
func TestVerifyRejected(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		stderr                string
		notVerified, unsigned bool
	}{
		{"unsigned", "Error: no signatures found\nerror during command execution: no signatures found", true, true},
		// An image that is not there is not an unsigned image: there is
		// nothing there to have been signed.
		{"no such tag", "Error: image tag not found: GET http://mm-1:5001/v2/absent/manifests/latest: MANIFEST_UNKNOWN: manifest unknown; map[Tag:latest]", true, false},
		// What cosign v3 says for a key that signed none of the signatures,
		// and what v2 said, since both are in use.
		{"untrusted key", "Error: no matching attestations: failed to verify signature: could not verify envelope: accepted signatures do not match threshold, Found: 0, Expected 1", true, false},
		{"wrong key", "Error: no matching signatures: invalid signature when validating ASN.1 encoded signature", true, false},
		{"wrong identity", "Error: none of the expected identities matched what was in the certificate", true, false},
		// A refusal cosign words in some way this package has never seen is
		// still a refusal, so that a release that rewords one cannot turn a
		// rejected signature into an infrastructure error, which would stop a
		// policy with several signers from trying the next one.
		{"an unfamiliar refusal", "Error: the signature is wrong in a way cosign has yet to invent", true, false},
		// A failure to reach the registry, or to be allowed to read it, is not
		// a verdict on the image and must not be reported as one.
		{"registry down", "Error: Get \"http://mm-1:5001/v2/\": dial tcp: connection refused", false, false},
		{"name does not resolve", "Error: Get \"http://mm-1:5001/v2/\": dial tcp: lookup mm-1: no such host", false, false},
		{"tls", "Error: Get \"https://mm-1:5001/v2/\": tls: failed to verify certificate: x509: certificate signed by unknown authority", false, false},
		{"not permitted", "Error: GET http://mm-1:5001/v2/linux-ci/manifests/latest: UNAUTHORIZED: authentication required", false, false},
		{"registry broken", "Error: unexpected status code 503 Service Unavailable", false, false},
	} {
		fakeCosign(t, "", tc.stderr, 1)
		policy := ociimage.Policy{Signers: []ociimage.Signer{{Key: "k.pub"}}}
		_, err := cosignverify.New().Verify(t.Context(), testRef(t), policy)
		if err == nil {
			t.Errorf("%v: Verify succeeded", tc.name)
			continue
		}
		if got := errors.Is(err, ociimage.ErrNotVerified); got != tc.notVerified {
			t.Errorf("%v: ErrNotVerified: got %v, want %v (%v)", tc.name, got, tc.notVerified, err)
		}
		if got := errors.Is(err, ociimage.ErrUnsigned); got != tc.unsigned {
			t.Errorf("%v: ErrUnsigned: got %v, want %v (%v)", tc.name, got, tc.unsigned, err)
		}
		if !strings.Contains(err.Error(), "mm-1:5001") {
			t.Errorf("%v: the error does not say what was being verified: %v", tc.name, err)
		}
	}
}

// TestVerifyManySigners covers a policy that trusts more than one signer,
// which is what a key being rotated looks like: the image is accepted if any
// one of them accepts it, and each is tried in turn until one does.
func TestVerifyManySigners(t *testing.T) {
	dir := t.TempDir()
	// A stub that rejects the old key and accepts the new one.
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = \"new.pub\" ]; then\n" +
		"    printf '%s' " + strconv.Quote(payloadJSON(testDigest, "")) + "\n" +
		"    exit 0\n" +
		"  fi\n" +
		"done\n" +
		"echo 'Error: no matching signatures' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "cosign"), []byte(script), 0700); err != nil { //nolint:gosec // test stub must be executable
		t.Fatalf("writing the cosign stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	policy := ociimage.Policy{Signers: []ociimage.Signer{{Key: "old.pub"}, {Key: "new.pub"}}}
	res, err := cosignverify.New().Verify(t.Context(), testRef(t), policy)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got, want := res.Signatures[0].Key, "new.pub"; got != want {
		t.Errorf("key: got %v, want %v", got, want)
	}

	// None of the signers accepting it is a rejection that names them all.
	policy.Signers = []ociimage.Signer{{Key: "old.pub"}, {Key: "older.pub"}}
	_, err = cosignverify.New().Verify(t.Context(), testRef(t), policy)
	if !errors.Is(err, ociimage.ErrNotVerified) {
		t.Errorf("got %v, want ErrNotVerified", err)
	}
	for _, want := range []string{"signers[0]", "signers[1]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %v: %v", want, err)
		}
	}
}

// TestVerifyKeyless covers the identity of a keyless signature, which cosign
// reports in the payload along with the log entry that makes it verifiable.
func TestVerifyKeyless(t *testing.T) {
	optional := `{"Subject":"cnicolaou@cloudeng.io","Issuer":"https://accounts.google.com",` +
		`"tier":"trusted","Bundle":{"SignedEntryTimestamp":"MEUC"}}`
	fakeCosign(t, payloadJSON(testDigest, optional), "", 0)
	policy := ociimage.Policy{
		Signers:         []ociimage.Signer{{SubjectRegexp: ".*@cloudeng[.]io", IssuerRegexp: "https://.*"}},
		TransparencyLog: true,
	}

	res, err := cosignverify.New().Verify(t.Context(), testRef(t), policy)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	sig := res.Signatures[0]
	if got, want := sig.Subject, "cnicolaou@cloudeng.io"; got != want {
		t.Errorf("subject: got %v, want %v", got, want)
	}
	if got, want := sig.Issuer, "https://accounts.google.com"; got != want {
		t.Errorf("issuer: got %v, want %v", got, want)
	}
	if sig.Key != "" {
		t.Errorf("a keyless signature reported the key %q", sig.Key)
	}
	if !sig.Logged {
		t.Error("a signature with a log entry is not reported as logged")
	}
	// The identity is not an annotation, the rest of optional is.
	if got, want := sig.Annotations, map[string]string{"tier": "trusted"}; len(got) != len(want) || got["tier"] != want["tier"] {
		t.Errorf("annotations: got %v, want %v", got, want)
	}
}

// TestVerifyBadOutput covers cosign succeeding but saying nothing usable,
// which must not be taken for a verified image.
func TestVerifyBadOutput(t *testing.T) {
	for _, tc := range []struct{ name, stdout string }{
		{"no signature", "[]"},
		{"no digest", payloadJSON("", "")},
		{"not json", "Verification succeeded\n"},
		{"two digests", `[{"critical":{"image":{"docker-manifest-digest":"sha256:aa"}}},` +
			`{"critical":{"image":{"docker-manifest-digest":"sha256:bb"}}}]`},
	} {
		fakeCosign(t, tc.stdout, "", 0)
		policy := ociimage.Policy{Signers: []ociimage.Signer{{Key: "k.pub"}}}
		if _, err := cosignverify.New().Verify(t.Context(), testRef(t), policy); err == nil {
			t.Errorf("%v: Verify succeeded on %q", tc.name, tc.stdout)
		}
	}
}

// TestVerifyNoSigners covers a policy that trusts nobody, which
// ociimage.Verify rejects before a verifier sees it: nothing is ever accepted
// by default.
func TestVerifyNoSigners(t *testing.T) {
	fakeCosign(t, payloadJSON(testDigest, ""), "", 0)
	_, err := cosignverify.New().Verify(t.Context(), testRef(t), ociimage.Policy{})
	if !errors.Is(err, ociimage.ErrNotVerified) {
		t.Errorf("got %v, want ErrNotVerified", err)
	}
}

// TestVerifyAbandoned covers a verification that was given up rather than
// carried out, which says nothing about the image: a cancelled cosign is not
// an image that failed its policy.
func TestVerifyAbandoned(t *testing.T) {
	// A cosign that never finishes, so that the context runs out first.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cosign"), []byte("#!/bin/sh\nsleep 10\n"), 0700); err != nil { //nolint:gosec // test stub must be executable
		t.Fatalf("writing the cosign stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	policy := ociimage.Policy{Signers: []ociimage.Signer{{Key: "k.pub"}}}
	_, err := cosignverify.New().Verify(ctx, testRef(t), policy)
	if err == nil {
		t.Fatal("Verify succeeded after the context was cancelled")
	}
	if errors.Is(err, ociimage.ErrNotVerified) || errors.Is(err, ociimage.ErrUnsigned) {
		t.Errorf("a cancelled verification was reported as a verdict on the image: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the cause was lost: %v", err)
	}
}

// TestVerifyNoCosign covers cosign not being installed, which says nothing
// about the image and so is not a verification failure.
func TestVerifyNoCosign(t *testing.T) {
	policy := ociimage.Policy{Signers: []ociimage.Signer{{Key: "k.pub"}}}
	v := cosignverify.New(cosignverify.WithBinary(filepath.Join(t.TempDir(), "no-such-cosign")))
	_, err := v.Verify(t.Context(), testRef(t), policy)
	if err == nil {
		t.Fatal("Verify succeeded without cosign")
	}
	if errors.Is(err, ociimage.ErrNotVerified) || errors.Is(err, ociimage.ErrUnsigned) {
		t.Errorf("a missing cosign was reported as a verdict on the image: %v", err)
	}
}
