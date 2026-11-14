// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

import (
	"context"
	"sync"
	"time"

	"cloudeng.io/vms/ociimage"
	basepool "cloudeng.io/vms/vmspool"
)

// ImageVerification is what a pool reports about the signature of the image
// its VMs are created from: whether one is required of it, and what came of
// the most recent check.
//
// Required is the configuration and the rest is history, which is why an
// unverified pool reports nothing else: an image that is never checked is not
// known to be signed, and is not known to be unsigned either.
type ImageVerification struct {
	// Required is true when the pool's configuration requires the image to
	// carry a signature that satisfies a policy, see TartConfig.Verify.
	Required bool
	// Signers is the number of signers that policy trusts and TransparencyLog
	// whether it requires the signature to be in a transparency log.
	Signers         int
	TransparencyLog bool
	// Verified is the image as it was last accepted, named by the digest that
	// carried the signature. It is empty until a check succeeds, and empty
	// again after one fails: it names an image that may be used, so it must
	// not outlive the last verification that said so.
	Verified string
	// Err is why the most recent check failed, empty if it succeeded or if
	// none has run yet.
	Err string
	// At is when the most recent check finished, zero if none has run. A pool
	// verifies when it creates a VM, so this is also the last time one was
	// created, or attempted.
	At time.Time
}

// Signed reports whether the image was accepted by the most recent check,
// which is the question "is the image this pool clones signed as it must be".
// It is false for a pool that does not verify, and for one that has not
// created a VM yet.
func (v ImageVerification) Signed() bool {
	return v.Verified != "" && v.Err == ""
}

// Checked reports whether any verification has been carried out, which
// distinguishes a pool that has not created a VM yet from one whose image was
// rejected.
func (v ImageVerification) Checked() bool {
	return !v.At.IsZero()
}

// recordingVerifier wraps an ociimage.Verifier and keeps what the most recent
// verification decided, so that a pool can report whether the image it clones
// is signed as its policy requires without verifying it again, see
// PoolSnapshot.Verification.
//
// Verification happens whenever a VM is created, by the pull or by the clone
// depending on how the pool is configured, see newTartBackend; both go through
// the verifier, which is why the record is kept here rather than at either
// call site.
type recordingVerifier struct {
	verifier ociimage.Verifier
	mu       sync.Mutex
	state    ImageVerification
}

var _ ociimage.Verifier = (*recordingVerifier)(nil)

// newRecordingVerifier returns a verifier that records its results, reporting
// the policy it enforces from the outset: a pool requires a signature from the
// moment it is configured to, not from the first VM it creates.
func newRecordingVerifier(v ociimage.Verifier, policy ociimage.Policy) *recordingVerifier {
	return &recordingVerifier{
		verifier: v,
		state: ImageVerification{
			Required:        true,
			Signers:         len(policy.Signers),
			TransparencyLog: policy.TransparencyLog,
		},
	}
}

// Verify implements ociimage.Verifier.
func (r *recordingVerifier) Verify(ctx context.Context, ref ociimage.Reference, policy ociimage.Policy) (ociimage.Verified, error) {
	res, err := r.verifier.Verify(ctx, ref, policy)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.At = time.Now()
	if err != nil {
		r.state.Verified, r.state.Err = "", err.Error()
		return res, err
	}
	r.state.Verified, r.state.Err = res.Reference.String(), ""
	return res, nil
}

// verification returns what has been recorded.
func (r *recordingVerifier) verification() ImageVerification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// imageVerifier is implemented by a backend that can report on the signature
// of its image, which is how a snapshot obtains it without knowing which
// backend a pool uses.
type imageVerifier interface {
	ImageVerification() ImageVerification
}

// verificationOf returns what a pool's backend reports about the signature of
// its image, or the zero value for a backend that does not verify one. A
// pool's provider is wrapped for caching, so the wrapped provider is asked as
// well, see basepool.CachingProvider.
func verificationOf(provider basepool.Provider) ImageVerification {
	if cached, ok := provider.(*basepool.CachingProvider); ok && cached.Provider != nil {
		provider = cached.Provider
	}
	if v, ok := provider.(imageVerifier); ok {
		return v.ImageVerification()
	}
	return ImageVerification{}
}
