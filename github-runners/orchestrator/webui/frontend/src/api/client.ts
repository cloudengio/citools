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

export const configFileURL = `${BASE}/config/file`
export const eventsURL = `${BASE}/events`

export const logURL = (workflow: string, artifactId: string) =>
  `${BASE}/workflows/${encodeURIComponent(workflow)}/logs/${encodeURIComponent(artifactId)}`
