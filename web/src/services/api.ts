const API_BASE = '/api'

// --- Types ---

export interface ProjectSummary {
  id: string
  title: string
  status: 'active' | 'stopped' | 'completed'
  created_at: string
  reason: ReasonLease | null
  fact_count: number
  intent_count: number
  working_intent_count: number
  unclaimed_intent_count: number
  hint_count: number
}

export interface ReasonLease {
  worker: string
  trigger: string
  started_at: string
  last_heartbeat_at: string
}

export interface Fact {
  id: string
  description: string
}

export interface Intent {
  id: string
  from: string[]
  to: string | null
  description: string
  creator: string
  worker: string | null
  last_heartbeat_at: string | null
  created_at: string
  concluded_at: string | null
}

export interface Hint {
  id: string
  content: string
  creator: string
  created_at: string
}

export interface ProjectDetail {
  project: {
    id: string
    title: string
    status: 'active' | 'stopped' | 'completed'
    created_at: string
    reason: ReasonLease | null
  }
  facts: Fact[]
  intents: Intent[]
  hints: Hint[]
}

export interface Settings {
  intent_timeout: number
  reason_timeout: number
}

// --- API helper ---

async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const opts: RequestInit = { method, headers: { 'Content-Type': 'application/json' } }
  if (body) opts.body = JSON.stringify(body)
  const r = await fetch(path, opts)
  if (r.status === 204) return null as T
  const data = await r.json()
  if (!r.ok) {
    const msg = typeof data.detail === 'string' ? data.detail : `HTTP ${r.status}`
    throw new Error(msg)
  }
  return data as T
}

async function fetchText(path: string): Promise<string> {
  const r = await fetch(path)
  const text = await r.text()
  if (!r.ok) throw new Error(text || `HTTP ${r.status}`)
  return text
}

// --- Settings ---

export function getSettings() {
  return api<Settings>('GET', `${API_BASE}/settings`)
}

export function updateSettings(data: Settings) {
  return api<Settings>('PUT', `${API_BASE}/settings`, data)
}

// --- Projects ---

export function listProjects() {
  return api<ProjectSummary[]>('GET', `${API_BASE}/projects`)
}

export function getProject(id: string) {
  return api<ProjectDetail>('GET', `${API_BASE}/projects/${id}`)
}

export function createProject(data: {
  title: string
  origin: string
  goal: string
  hints?: { content: string; creator: string }[]
}) {
  return api<ProjectDetail>('POST', `${API_BASE}/projects`, data)
}

export function deleteProject(id: string) {
  return api<void>('DELETE', `${API_BASE}/projects/${id}`)
}

export function updateProjectStatus(id: string, status: string) {
  return api<{ status: string; reason: ReasonLease | null }>('PUT', `${API_BASE}/projects/${id}/status`, { status })
}

export function updateProjectTitle(id: string, title: string) {
  return api<{ title: string }>('PUT', `${API_BASE}/projects/${id}/title`, { title })
}

export function completeProject(id: string, data: { from: string[]; description: string; worker: string }) {
  return api<unknown>('POST', `${API_BASE}/projects/${id}/complete`, data)
}

export function reopenProject(id: string, data: { description: string; creator: string }) {
  return api<unknown>('POST', `${API_BASE}/projects/${id}/reopen`, data)
}

// --- Intents ---

export function createIntent(projectId: string, data: {
  from: string[]
  description: string
  creator: string
  worker: string | null
}) {
  return api<Intent>('POST', `${API_BASE}/projects/${projectId}/intents`, data)
}

export function heartbeatIntent(projectId: string, intentId: string, worker: string) {
  return api<unknown>('POST', `${API_BASE}/projects/${projectId}/intents/${intentId}/heartbeat`, { worker })
}

export function releaseIntent(projectId: string, intentId: string, worker: string) {
  return api<unknown>('POST', `${API_BASE}/projects/${projectId}/intents/${intentId}/release`, { worker })
}

export function concludeIntent(projectId: string, intentId: string, data: { worker: string; description: string }) {
  return api<unknown>('POST', `${API_BASE}/projects/${projectId}/intents/${intentId}/conclude`, data)
}

// --- Hints ---

export function createHint(projectId: string, content: string, creator: string) {
  return api<Hint>('POST', `${API_BASE}/projects/${projectId}/hints`, { content, creator })
}

// --- Export ---

export function exportProject(projectId: string, format: 'yaml' | 'timeline') {
  return fetchText(`${API_BASE}/projects/${projectId}/export?format=${format}`)
}

// --- Task Events (timeline replay) ---

export interface TaskEvent {
  id: number
  project_id: string
  task_type: string
  intent_id: string
  worker: string
  phase: string
  prompt: string
  output: string
  error: string
  duration_ms: number
  created_at: string
}

export function listTaskEvents(projectId: string, params?: { task_type?: string; worker?: string; phase?: string; limit?: number; offset?: number }) {
  const qs = new URLSearchParams()
  if (params?.task_type) qs.set('task_type', params.task_type)
  if (params?.worker) qs.set('worker', params.worker)
  if (params?.phase) qs.set('phase', params.phase)
  if (params?.limit) qs.set('limit', String(params.limit))
  if (params?.offset) qs.set('offset', String(params.offset))
  const q = qs.toString()
  return api<TaskEvent[]>('GET', `${API_BASE}/projects/${projectId}/events${q ? '?' + q : ''}`)
}

export function getTaskEvent(projectId: string, eventId: number) {
  return api<TaskEvent>('GET', `${API_BASE}/projects/${projectId}/events/${eventId}`)
}

// --- Dispatchers ---

export interface DispatcherWorkerInfo {
  name: string
  type: string
  task_types: string[]
  max_running: number
  running: number
}

export interface DispatcherRuntimeInfo {
  interval: number
  max_workers: number
  max_running_projects: number
  max_project_workers: number
  prompt_group: string
}

export interface DispatcherInstance {
  id: string
  name: string
  status: 'running' | 'stopped' | 'error'
  started_at: string | null
  error: string
  workers: DispatcherWorkerInfo[]
  runtime: DispatcherRuntimeInfo
  running_tasks: number
  admitted_count: number
}

export function listDispatchers() {
  return api<DispatcherInstance[]>('GET', `${API_BASE}/dispatchers`)
}

export function getDispatcher(id: string) {
  return api<DispatcherInstance>('GET', `${API_BASE}/dispatchers/${id}`)
}

export function createDispatcher(data: { name: string; config_path?: string }) {
  return api<DispatcherInstance>('POST', `${API_BASE}/dispatchers`, data)
}

export function startDispatcher(id: string) {
  return api<DispatcherInstance>('POST', `${API_BASE}/dispatchers/${id}/start`)
}

export function stopDispatcher(id: string) {
  return api<DispatcherInstance>('POST', `${API_BASE}/dispatchers/${id}/stop`)
}

export function deleteDispatcher(id: string) {
  return api<void>('DELETE', `${API_BASE}/dispatchers/${id}`)
}
