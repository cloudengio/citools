import { useCallback, useEffect, useState } from 'react'
import {
  eventsURL,
  getConfig,
  getPools,
  getWorkflows,
  type ConfigSummary,
  type PoolStatus,
  type WorkflowStatus,
} from './client'

// useOrchestrator loads the initial state over REST and then uses the SSE
// /events stream purely as a change signal: on any server-sent event it
// debounces a full REST refresh of pools and workflows. Re-fetching a
// consistent snapshot (rather than mutating from individual events) keeps
// additions, updates AND deletions correct with minimal client logic.
export function useOrchestrator() {
  const [config, setConfig] = useState<ConfigSummary | null>(null)
  const [pools, setPools] = useState<PoolStatus[]>([])
  const [workflows, setWorkflows] = useState<WorkflowStatus[]>([])
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      const [p, w] = await Promise.all([getPools(), getWorkflows()])
      setPools(p ?? [])
      setWorkflows(w ?? [])
      setError(null)
    } catch (e) {
      setError(String(e))
    }
  }, [])

  useEffect(() => {
    getConfig()
      .then(setConfig)
      .catch((e) => setError(String(e)))
    void refresh()

    const es = new EventSource(eventsURL)
    let timer: number | undefined
    const onChange = () => {
      setConnected(true)
      if (timer !== undefined) return
      timer = window.setTimeout(() => {
        timer = undefined
        void refresh()
      }, 300)
    }
    es.addEventListener('hello', onChange)
    es.addEventListener('pool', onChange)
    es.addEventListener('workflow', onChange)
    es.onerror = () => setConnected(false)

    return () => {
      es.close()
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [refresh])

  return { config, pools, workflows, connected, error, refresh }
}
