import { useState } from 'react'
import { CANCELLABLE_STATES, cancelWorkflow, logURL, type WorkflowStatus } from '../api/client'
import { Badge } from './Badge'
import { LiveDuration } from './LiveDuration'
import { fmtTime } from '../format'

function CancelButton({ wf }: { wf: WorkflowStatus }) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  if (!CANCELLABLE_STATES.includes(wf.state)) {
    return <span className="muted">—</span>
  }

  const onClick = async () => {
    if (!window.confirm(`Cancel workflow "${wf.name}"? This cancels the GitHub run and deletes its VM.`)) {
      return
    }
    setBusy(true)
    setErr(null)
    try {
      await cancelWorkflow(wf.name)
      // The row updates via the SSE-driven refresh once the state changes.
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <button className="btn-danger sm" onClick={onClick} disabled={busy}>
        {busy ? 'Canceling…' : 'Cancel'}
      </button>
      {err && <div className="err-text">{err}</div>}
    </>
  )
}

function LogLinks({ wf }: { wf: WorkflowStatus }) {
  const logs = wf.logs ?? []
  if (logs.length === 0) return <span className="muted">—</span>
  return (
    <div className="log-links">
      {logs.map((l) => {
        const canView = l.id === 'job' || (l.content_type?.startsWith('text/') ?? false)
        return (
          <div key={l.id} className="log-item">
            <span className="mono">{l.id}:</span>{' '}
            {canView ? (
              <>
                <a
                  className="loglink"
                  href={logURL(wf.name, l.id, true)}
                  target="_blank"
                  rel="noreferrer"
                  title={`View ${l.filename ?? l.id}`}
                >
                  view
                </a>
                <span className="muted">·</span>{' '}
                <a
                  className="loglink"
                  href={logURL(wf.name, l.id)}
                  download
                  title={`Download ${l.filename ?? l.id}`}
                >
                  download
                </a>
              </>
            ) : (
              <a
                className="loglink"
                href={logURL(wf.name, l.id)}
                download
                title={`Download ${l.filename ?? l.id}`}
              >
                download
              </a>
            )}
          </div>
        )
      })}
    </div>
  )
}

function WorkflowRow({ wf }: { wf: WorkflowStatus }) {
  return (
    <tr>
      <td>
        <Badge value={wf.state} />
        {wf.state === 'vm_completed' && <div className="hint">awaiting GitHub</div>}
      </td>
      <td>
        <div className="mono">{wf.name}</div>
        {wf.error && <div className="err-text">{wf.error}</div>}
        {wf.result && <div className="muted">{wf.result}</div>}
      </td>
      <td>
        {wf.repo_url ? (
          <a href={wf.repo_url} target="_blank" rel="noreferrer">{wf.repo_full_name ?? wf.repo_url}</a>
        ) : (
          wf.repo_full_name ?? '—'
        )}
        <div className="muted">{wf.workflow_name}{wf.job_name ? ` / ${wf.job_name}` : ''}</div>
      </td>
      <td>
        {(wf.labels ?? []).map((l) => <span key={l} className="chip sm">{l}</span>)}
      </td>
      <td className="mono">{wf.pool ?? '—'}</td>
      <td className="mono">{wf.vm_id ?? '—'}</td>
      <td>{fmtTime(wf.queued_at)}</td>
      <td><LiveDuration from={wf.started_at} to={wf.completed_at} /></td>
      <td><LogLinks wf={wf} /></td>
      <td><CancelButton wf={wf} /></td>
    </tr>
  )
}

export function WorkflowsView({ workflows }: { workflows: WorkflowStatus[] }) {
  return (
    <section>
      <h2>Workflows <span className="muted">({workflows.length})</span></h2>
      {workflows.length === 0 ? (
        <p className="muted">No running or recently-completed workflows.</p>
      ) : (
        <table className="tbl">
          <thead>
            <tr>
              <th>State</th><th>Runner</th><th>Repository</th><th>Labels</th>
              <th>Pool</th><th>VM</th><th>Queued</th><th>Duration</th><th>Logs</th><th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {workflows.map((wf) => <WorkflowRow key={wf.name} wf={wf} />)}
          </tbody>
        </table>
      )}
    </section>
  )
}
