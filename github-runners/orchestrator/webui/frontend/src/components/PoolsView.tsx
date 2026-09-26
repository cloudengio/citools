import type { PoolStatus, VMStatus } from '../api/client'
import { Badge } from './Badge'
import { fmtTime } from '../format'

function VMRow({ vm }: { vm: VMStatus }) {
  const isCQ = vm.location === 'completion_queue' || vm.state === 'completion_queue'
  return (
    <tr>
      <td className="mono">{vm.name ?? vm.id}</td>
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

function Pool({ pool }: { pool: PoolStatus }) {
  const vms = pool.vms ?? []
  const available = pool.available ?? 0
  const acquired = pool.acquired ?? 0
  const pending = pool.pending ?? 0
  const inCQ = vms.filter((v) => v.location === 'completion_queue' || v.state === 'completion_queue').length

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
        <span className="spacer" />
        <span className="chip">size {pool.size}</span>
        <span className="chip">available {available}</span>
        <span className="chip">acquired {acquired}</span>
        {pending > 0 && <span className="chip">pending {pending}</span>}
        <span className="chip">completion queue {inCQ}</span>
        <span className="chip">total {vms.length}</span>
      </div>
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

export function PoolsView({ pools }: { pools: PoolStatus[] }) {
  return (
    <section>
      <h2>VM Pools</h2>
      {pools.length === 0 ? (
        <p className="muted">No pools configured.</p>
      ) : (
        pools.map((p) => <Pool key={p.name} pool={p} />)
      )}
    </section>
  )
}
