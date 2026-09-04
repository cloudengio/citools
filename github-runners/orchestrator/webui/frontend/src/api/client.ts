// Typed API client. The domain types are re-exported from the generated
// openapi-typescript models (types.gen.ts) so the UI stays in lock-step with
// openapi.yaml — run `npm run gen` after changing the spec.
import type { components } from './types.gen'

export type ConfigSummary = components['schemas']['ConfigSummary']
export type PoolStatus = components['schemas']['PoolStatus']
export type VMStatus = components['schemas']['VMStatus']
export type WorkflowStatus = components['schemas']['WorkflowStatus']
export type LogArtifact = components['schemas']['LogArtifact']
export type WorkflowState = components['schemas']['WorkflowState']
export type VMState = components['schemas']['VMState']
export type ServiceStatus = components['schemas']['ServiceStatus']
export type BuildInfo = components['schemas']['BuildInfo']

export const BASE = '/api/v1'

async function getJSON<T>(path: string): Promise<T> {
  const resp = await fetch(BASE + path)
  if (!resp.ok) {
    throw new Error(`${path}: ${resp.status} ${resp.statusText}`)
  }
  return (await resp.json()) as T
}

export const getConfig = () => getJSON<ConfigSummary>('/config')
export const getPools = () => getJSON<PoolStatus[]>('/pools')
export const getWorkflows = () => getJSON<WorkflowStatus[]>('/workflows')
export const getBuildInfo = () => getJSON<BuildInfo>('/buildinfo')

export const configFileURL = `${BASE}/config/file`
export const eventsURL = `${BASE}/events`

export const logURL = (workflow: string, artifactId: string, view?: boolean) => {
  const u = `${BASE}/workflows/${encodeURIComponent(workflow)}/logs/${encodeURIComponent(artifactId)}`
  return view ? `${u}?view=true` : u
}

// cancelWorkflow requests cancellation of a running workflow job, which cancels
// its GitHub run and tears down its VM. Rejects with the server's error message.
export async function cancelWorkflow(name: string): Promise<void> {
  const resp = await fetch(
    `${BASE}/workflows/${encodeURIComponent(name)}/cancel`,
    { method: 'POST' },
  )
  if (!resp.ok) {
    let msg = `${resp.status} ${resp.statusText}`
    try {
      const body = (await resp.json()) as { error?: string }
      if (body?.error) msg = body.error
    } catch {
      /* no JSON body */
    }
    throw new Error(msg)
  }
}

// CANCELLABLE_STATES are the workflow states for which cancellation is offered.
export const CANCELLABLE_STATES: WorkflowState[] = ['queued', 'acquiring', 'running']

export const getServiceStatus = () => getJSON<ServiceStatus>('/service')

export async function restartService(): Promise<void> {
  const resp = await fetch(`${BASE}/service/restart`, { method: 'POST' })
  if (!resp.ok) {
    let msg = `${resp.status} ${resp.statusText}`
    try {
      const body = (await resp.json()) as { error?: string }
      if (body?.error) msg = body.error
    } catch {
      /* no JSON body */
    }
    throw new Error(msg)
  }
}

export async function uninstallService(): Promise<void> {
  const resp = await fetch(`${BASE}/service/uninstall`, { method: 'POST' })
  if (!resp.ok) {
    let msg = `${resp.status} ${resp.statusText}`
    try {
      const body = (await resp.json()) as { error?: string }
      if (body?.error) msg = body.error
    } catch {
      /* no JSON body */
    }
    throw new Error(msg)
  }
}
