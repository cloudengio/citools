import { useState } from 'react'
import { configFileURL, type ConfigSummary } from '../api/client'

export function ConfigView({ config }: { config: ConfigSummary | null }) {
  const [open, setOpen] = useState(false)
  if (!config) return null
  const g = config.global ?? {}
  return (
    <section>
      <h2>
        Configuration
        <button className="link" onClick={() => setOpen((o) => !o)}>
          {open ? 'hide' : 'show'}
        </button>
        <a className="btn" href={configFileURL} download>Download config file</a>
      </h2>
      <div className="muted mono">{config.config_file}</div>
      {open && (
        <div className="card">
          <div className="grid2">
            <div><span className="muted">tmp dir</span><div className="mono">{g.tmp_dir ?? '—'}</div></div>
            <div><span className="muted">completion queue</span><div className="mono">{g.completion_queue_size ?? '—'}</div></div>
            <div><span className="muted">success retention</span><div className="mono">{g.successful_vm_retention_period ?? '—'}</div></div>
            <div><span className="muted">failed retention</span><div className="mono">{g.failed_vm_retention_period ?? '—'}</div></div>
            <div><span className="muted">webhook relay</span><div className="mono">{config.webhook?.relay_url ?? '—'}</div></div>
          </div>

          <h4>Repositories</h4>
          <table className="tbl">
            <thead><tr><th>Owner/Repo</th><th>Runner</th><th>Labels</th><th>Pool</th></tr></thead>
            <tbody>
              {(config.repositories ?? []).flatMap((r) =>
                (r.runners ?? []).map((run, i) => (
                  <tr key={`${r.owner}/${r.repo}/${run.name_prefix}/${i}`}>
                    <td className="mono">{r.owner}/{r.repo}</td>
                    <td className="mono">{run.name_prefix ?? '—'}</td>
                    <td>{(run.labels ?? []).map((l) => <span key={l} className="chip sm">{l}</span>)}</td>
                    <td className="mono">{run.vm_pool ?? '—'}</td>
                  </tr>
                )),
              )}
            </tbody>
          </table>

          <h4>Pools</h4>
          <table className="tbl">
            <thead><tr><th>Name</th><th>Image</th><th>Size</th><th>Runner dir</th></tr></thead>
            <tbody>
              {(config.pools ?? []).map((p) => (
                <tr key={p.name}>
                  <td className="mono">{p.name}</td>
                  <td className="mono">{p.image ?? '—'}</td>
                  <td className="mono">{p.size ?? '—'}</td>
                  <td className="mono">{p.runner_dir ?? '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
