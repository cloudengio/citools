import { useState } from 'react'
import { configFileURL, type BuildInfo, type ConfigSummary } from '../api/client'
import { fmtTime } from '../format'

export function ConfigView({
  config,
  buildInfo,
}: {
  config: ConfigSummary | null
  buildInfo?: BuildInfo | null
}) {
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
          {buildInfo && (
            <>
              <h4>Build Information</h4>
              <div className="grid2" style={{ marginBottom: 16 }}>
                <div><span className="muted">Go version</span><div className="mono">{buildInfo.go_version}</div></div>
                <div>
                  <span className="muted">Commit revision</span>
                  <div className="mono">
                    {buildInfo.revision ? (
                      <span title={buildInfo.revision}>
                        {buildInfo.revision_short ?? buildInfo.revision.slice(0, 12)}
                        {buildInfo.modified ? ' (modified/dirty)' : ''}
                      </span>
                    ) : '—'}
                  </div>
                </div>
                {buildInfo.modified ? (
                  <div><span className="muted">Build date</span><div className="mono">{fmtTime(buildInfo.build_time)}</div></div>
                ) : (
                  <div><span className="muted">Commit date</span><div className="mono">{fmtTime(buildInfo.revision_time)}</div></div>
                )}
                <div><span className="muted">Target OS / Arch</span><div className="mono">{buildInfo.os}/{buildInfo.arch}</div></div>
                {buildInfo.version && buildInfo.version !== '(devel)' && (
                  <div><span className="muted">Version</span><div className="mono">{buildInfo.version}</div></div>
                )}
                {buildInfo.path && (
                  <div><span className="muted">Module path</span><div className="mono">{buildInfo.path}</div></div>
                )}
              </div>
            </>
          )}

          <h4>Global Settings</h4>
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
