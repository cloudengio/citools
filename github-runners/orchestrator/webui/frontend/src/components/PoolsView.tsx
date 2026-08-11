import type { PoolStatus, VMStatus } from '../api/client'
import { Badge } from './Badge'
import { fmtTime } from '../format'

function VMRow({ vm }: { vm: VMStatus }) {
  return (
    <tr>
      <td className="mono">{vm.name ?? vm.id}</td>
      <td><Badge value={vm.state} /></td>
      <td className="mono">{vm.last_event ?? '—'}</td>
      <td>{fmtTime(vm.updated_at)}</td>
    </tr>
  )
}

function Pool({ pool }: { pool: PoolStatus }) {
  const vms = pool.vms ?? []
  const acquired = vms.filter((v) => v.state === 'acquired').length
  const available = vms.filter((v) => v.state === 'available').length
  return (
    <div className="card">
      <div className="card-head">
        <h3>{pool.name}</h3>
        <span className="muted mono">{pool.image ?? ''}</span>
        <span className="spacer" />
        <span className="chip">size {pool.size}</span>
        <span className="chip">available {available}</span>
        <span className="chip">acquired {acquired}</span>
        <span className="chip">total {vms.length}</span>
      </div>
      {vms.length === 0 ? (
        <p className="muted">No VMs currently present.</p>
      ) : (
        <table className="tbl">
          <thead>
            <tr><th>VM</th><th>State</th><th>Last state</th><th>Updated</th></tr>
          </thead>
          <tbody>
            {vms.map((vm) => <VMRow key={vm.id} vm={vm} />)}
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
