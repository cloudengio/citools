import { useEffect } from 'react'
import { useOrchestrator } from './api/useOrchestrator'
import { PoolsView } from './components/PoolsView'
import { WorkflowsView } from './components/WorkflowsView'
import { ConfigView } from './components/ConfigView'
import { ServiceControls } from './components/ServiceControls'
import { fmtTime } from './format'

export default function App() {
  const { config, buildInfo, pools, workflows, connected, error, refresh } = useOrchestrator()
  const host = buildInfo?.hostname
  // The UI is usually reached over a forwarded port, so neither the URL nor the
  // tab says which orchestrator is being looked at: name the host in both.
  useEffect(() => {
    document.title = host ? `${host} · Runner Orchestrator` : 'Runner Orchestrator'
  }, [host])
  return (
    <div className="app">
      <header className="topbar">
        <div>
          <h1>
            GitHub Runner Orchestrator
            {host && <span className="host" title={`Running on ${host}`}>{host}</span>}
          </h1>
          {buildInfo && (
            <div className="build-info-bar">
              {buildInfo.version && buildInfo.version !== '(devel)' && (
                <span className="chip sm">{buildInfo.version}</span>
              )}
              {buildInfo.revision_short && (
                <span
                  className="mono chip sm"
                  title={`Commit: ${buildInfo.revision}${buildInfo.modified ? ' (modified/dirty)' : ''}`}
                >
                  git:{buildInfo.revision_short}{buildInfo.modified ? '*' : ''}
                </span>
              )}
              {buildInfo.modified ? (
                buildInfo.build_time && (
                  <span className="muted" title="Build time">
                    {fmtTime(buildInfo.build_time)}
                  </span>
                )
              ) : (
                buildInfo.revision_time && (
                  <span className="muted" title="Commit date">
                    {fmtTime(buildInfo.revision_time)}
                  </span>
                )
              )}
              <span className="muted">
                {buildInfo.os}/{buildInfo.arch}
              </span>
              <span className="muted">
                {buildInfo.go_version}
              </span>
            </div>
          )}
        </div>
        <span className="spacer" />
        <ServiceControls />
        <span className={`dot ${connected ? 'live' : 'down'}`} title={connected ? 'live (SSE connected)' : 'disconnected'} />
        <span className="conn">{connected ? 'live' : 'disconnected'}</span>
        <button className="btn" onClick={() => void refresh()}>Refresh</button>
      </header>
      {error && <div className="error">{error}</div>}
      <main>
        <PoolsView pools={pools} onRefresh={refresh} />
        <WorkflowsView workflows={workflows} />
        <ConfigView config={config} buildInfo={buildInfo} />
      </main>
    </div>
  )
}
