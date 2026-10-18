import { useState } from 'react'
import { refreshPool, type PoolStatus, type VMStatus } from '../api/client'
import { Badge } from './Badge'
import { fmtTime } from '../format'

function VMPopup({ vm }: { vm: VMStatus }) {
  return (
    <div className="hover-menu-wrapper" tabIndex={0} aria-label={`Configuration for ${vm.name ?? vm.id}`}>
      <span className="chip sm chip-interactive" title="View VM configuration">
        ℹ
      </span>
      <div className="hover-menu-dropdown">
        <div className="hover-menu-header">VM Configuration</div>
        <div className="hover-menu-grid">
          <span className="muted">Name</span>
          <span className="mono">{vm.name ?? vm.id}</span>
          {vm.num_cores !== undefined && vm.num_cores > 0 && (
            <>
              <span className="muted">CPUs</span>
              <span className="mono">{vm.num_cores} cores</span>
            </>
          )}
          {vm.mem_gib !== undefined && vm.mem_gib > 0 && (
            <>
              <span className="muted">Memory</span>
              <span className="mono">{vm.mem_gib} GB</span>
            </>
          )}
          {vm.disk_gib !== undefined && vm.disk_gib > 0 && (
            <>
              <span className="muted">Disk</span>
              <span className="mono">{vm.disk_gib} GB</span>
            </>
          )}
          {vm.os && (
            <>
              <span className="muted">OS</span>
              <span className="mono">{vm.os}</span>
            </>
          )}
          {vm.image && (
            <>
              <span className="muted">Image</span>
              <span className="mono">{vm.image}</span>
            </>
          )}
          {vm.runner_dir && (
            <>
              <span className="muted">Runner Dir</span>
              <span className="mono">{vm.runner_dir}</span>
            </>
          )}
          {vm.pool && (
            <>
              <span className="muted">Pool</span>
              <span className="mono">{vm.pool}</span>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

function VMRow({ vm }: { vm: VMStatus }) {
  const isCQ = vm.location === 'completion_queue' || vm.state === 'completion_queue'
  return (
    <tr>
      <td className="mono">
        <div className="vm-name-cell">
          <span>{vm.name ?? vm.id}</span>
          <VMPopup vm={vm} />
        </div>
      </td>
      <td>
        <span className={`chip sm ${isCQ ? 'chip-cq' : 'chip-pool'}`}>
          {isCQ ? 'completion queue' : 'in pool'}
        </span>
      </td>
      <td><Badge value={vm.state} /></td>
      <td className="mono">{vm.last_event ?? '—'}</td>
      <td>{fmtTime(vm.updated_at)}</td>
    </tr>
  )
}

function Pool({ pool, onRefresh }: { pool: PoolStatus; onRefresh?: () => Promise<void> }) {
  const [replacing, setReplacing] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  const vms = pool.vms ?? []
  const available = pool.available ?? 0
  const acquired = pool.acquired ?? 0
  const pending = pool.pending ?? 0
  const inCQ = vms.filter((v) => v.location === 'completion_queue' || v.state === 'completion_queue').length

  const handleReplace = async () => {
    const confirmed = window.confirm(
      `Remove all ${available} available VM(s) in pool "${pool.name}" and replace with newly cloned ones?`
    )
    if (!confirmed) return
    setReplacing(true)
    setActionError(null)
    try {
      await refreshPool(pool.name)
      if (onRefresh) await onRefresh()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err))
    } finally {
      setReplacing(false)
    }
  }

  const sortedVMs = [...vms].sort((a, b) => {
    const aCQ = a.location === 'completion_queue' || a.state === 'completion_queue' ? 1 : 0
    const bCQ = b.location === 'completion_queue' || b.state === 'completion_queue' ? 1 : 0
    if (aCQ !== bCQ) {
      return aCQ - bCQ
    }
    return (a.name ?? a.id).localeCompare(b.name ?? b.id)
  })

  return (
    <div className="card">
      <div className="card-head">
        <h3>{pool.name}</h3>
        {pool.kind && <span className="chip">{pool.kind}</span>}
        <span className="muted mono">{pool.image ?? ''}</span>
        <button
          className="btn btn-sm btn-replace-vms"
          onClick={handleReplace}
          disabled={available === 0 || replacing}
          title={
            available === 0
              ? 'No available VMs to replace'
              : `Remove all ${available} available VM(s) and clone fresh ones from ${pool.image ?? 'the image'}`
          }
        >
          {replacing ? 'Replacing...' : 'Replace Available VMs'}
        </button>
        <span className="spacer" />
        <span className="chip">size {pool.size}</span>
        <span className="chip">available {available}</span>
        <span className="chip">acquired {acquired}</span>
        {pending > 0 && <span className="chip">pending {pending}</span>}
        <span className="chip">completion queue {inCQ}</span>
        <span className="chip">total {vms.length}</span>
      </div>
      {actionError && <div className="err-text" style={{ marginBottom: 8 }}>{actionError}</div>}
      {vms.length === 0 ? (
        <p className="muted">No VMs currently present.</p>
      ) : (
        <table className="tbl">
          <thead>
            <tr>
              <th>VM</th>
              <th>Location</th>
              <th>State</th>
              <th>Last state</th>
              <th>Updated</th>
            </tr>
          </thead>
          <tbody>
            {sortedVMs.map((vm) => <VMRow key={vm.id} vm={vm} />)}
          </tbody>
        </table>
      )}
    </div>
  )
}

export function PoolsView({ pools, onRefresh }: { pools: PoolStatus[]; onRefresh?: () => Promise<void> }) {
  return (
    <section>
      <h2>VM Pools</h2>
      {pools.length === 0 ? (
        <p className="muted">No pools configured.</p>
      ) : (
        pools.map((p) => <Pool key={p.name} pool={p} onRefresh={onRefresh} />)
      )}
    </section>
  )
}
