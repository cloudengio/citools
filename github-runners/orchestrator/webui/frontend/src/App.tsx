import { useOrchestrator } from './api/useOrchestrator'
import { PoolsView } from './components/PoolsView'
import { WorkflowsView } from './components/WorkflowsView'
import { ConfigView } from './components/ConfigView'

export default function App() {
  const { config, pools, workflows, connected, error, refresh } = useOrchestrator()
  return (
    <div className="app">
      <header className="topbar">
        <h1>GitHub Runner Orchestrator</h1>
        <span className="spacer" />
        <span className={`dot ${connected ? 'live' : 'down'}`} title={connected ? 'live (SSE connected)' : 'disconnected'} />
        <span className="conn">{connected ? 'live' : 'disconnected'}</span>
        <button className="btn" onClick={() => void refresh()}>Refresh</button>
      </header>
      {error && <div className="error">{error}</div>}
      <main>
        <PoolsView pools={pools} />
        <WorkflowsView workflows={workflows} />
        <ConfigView config={config} />
      </main>
    </div>
  )
}
