// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

// HasCapacity reports whether any pool can supply a VM now or shortly, ie.
// whether there is any point in taking on more work. A set with no pools in
// it, or a closed one, has no capacity: there is nothing for any work to run
// on.
//
// A VM that is already held by a caller is not capacity for another one: a
// pool is of a fixed size. A VM being created is, even though Acquire has to
// wait for it: a pool starts replacing a VM as soon as the caller holding it
// releases it, so treating a replenishing pool as full would mean refusing
// work for as long as it takes to create a VM, repeatedly, whilst Acquire
// would have waited for it anyway.
//
// The pools are not interchangeable, since work names the pool it must run in,
// so capacity in one does not mean that a particular piece of work can be run.
// It is a measure of whether there is any work that can be taken on at all.
func (p *Pools) HasCapacity() bool {
	available, pending := p.AvailableAndPending()
	return available || pending
}
