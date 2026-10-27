// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package vmsclient

// Capacity is a snapshot of the ability of a set of pools to take on work: how
// many VMs can be acquired without waiting, and how many are being created and
// so can be acquired shortly. It deliberately says nothing about how many are
// already acquired: a pool is of a fixed size, so a VM that is held by a job is
// not capacity for another one.
type Capacity struct {
	// Pools is the number of pools the capacity was taken over.
	Pools int
	// Available is the number of VMs that Acquire can return immediately.
	Available int
	// Pending is the number of VMs that are being created, either to fill a
	// pool initially or to replace one that a job has finished with, and so
	// will be available shortly.
	Pending int
}

// Any reports whether a VM can be acquired now or shortly, ie. whether there
// is any point in taking on more work.
//
// Pending counts as capacity even though Acquire has to wait for it: a pool
// starts replacing a VM as soon as the job holding it releases it, so treating
// a replenishing pool as full would mean refusing work for as long as it takes
// to clone a VM, repeatedly, whilst Acquire would have waited for it anyway.
//
// A set with no pools in it has capacity: there is nothing to gate on, and
// reporting no capacity would silently stop a misconfigured orchestrator from
// doing anything at all rather than reporting why each job cannot be run.
func (c Capacity) Any() bool {
	return c.Pools == 0 || c.Available > 0 || c.Pending > 0
}

// Capacity reports the combined capacity of every pool. Note that the pools
// are not interchangeable: a job names the pool it must run in, so capacity in
// one pool does not mean that a particular job can be run. It is a measure of
// whether there is any work that can be taken on at all.
func (p *Pools) Capacity() Capacity {
	c := Capacity{Pools: len(p.pools)}
	for _, vmp := range p.pools {
		stats := vmp.Stats()
		c.Available += stats.Available
		c.Pending += stats.Pending
	}
	return c
}
