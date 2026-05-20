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
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 30000)
  opts.signal = controller.signal
  let r: Response
  try {
    r = await fetch(path, opts)
  } finally {
    clearTimeout(timeout)
  }
  if (r.status === 204) return null as T
  const data = await r.json()
  if (!r.ok) {
    const msg = (typeof data.detail === 'string' && data.detail) ? data.detail :
      (typeof data.error === 'string' && data.error) ? data.error :
        (typeof data.message === 'string' && data.message) ? data.message :
          `HTTP ${r.status}`
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

// --- SSE Streaming ---

export interface SSEEvent {
  type: string
  project_id: string
  data: unknown
}

export function connectSSE(onEvent: (event: SSEEvent) => void): EventSource {
  const es = new EventSource(`${API_BASE}/events/stream`)

  const eventTypes = ['task_dispatched', 'task_completed', 'task_failed', 'task_update',
    'intent_created', 'intent_concluded', 'fact_created', 'project_update', 'metrics']

  for (const type of eventTypes) {
    es.addEventListener(type, (e: MessageEvent) => {
      try {
        const parsed = JSON.parse(e.data) as SSEEvent
        onEvent(parsed)
      } catch { /* ignore parse errors */ }
    })
  }

  return es
}

// --- Metrics ---

export interface ProjectMetrics {
  id: string
  title: string
  status: string
  fact_count: number
  intent_count: number
  open_intent_count: number
  concluded_intent_count: number
  working_intent_count: number
  unclaimed_intent_count: number
  hint_count: number
  success_fact_count: number
  failure_fact_count: number
  blocker_fact_count: number
}

export interface MetricsResponse {
  total_projects: number
  active_projects: number
  completed_projects: number
  stopped_projects: number
  total_facts: number
  total_intents: number
  total_open_intents: number
  total_hints: number
  sse_clients: number
  projects: ProjectMetrics[]
}

export function getMetrics() {
  return api<MetricsResponse>('GET', `${API_BASE}/metrics`)
}

// --- CTFd Integration ---

export interface CTFdInstance {
  id: string
  name: string
  url: string
  created_at: string
}

export interface CTFdFile {
  id: number
  location: string
}

export interface CTFdChallenge {
  id: number
  name: string
  category: string
  description: string
  value: number
  solves: number
  type: string
  tags: string[]
  files: CTFdFile[]
  solved: boolean
  connection_info: string
  max_attempts: number
  attempts: number
}

export interface CTFdSubmitResponse {
  status: string
  message: string
}

export function listCTFdInstances() {
  return api<CTFdInstance[]>('GET', `${API_BASE}/ctfd/instances`)
}

export function addCTFdInstance(data: { name: string; url: string; token: string }) {
  return api<CTFdInstance>('POST', `${API_BASE}/ctfd/instances`, data)
}

export function deleteCTFdInstance(id: string) {
  return api<void>('DELETE', `${API_BASE}/ctfd/instances/${id}`)
}

export function listCTFdChallenges(instanceId: string) {
  return api<CTFdChallenge[]>('GET', `${API_BASE}/ctfd/instances/${instanceId}/challenges`)
}

export function getCTFdChallenge(instanceId: string, challengeId: number) {
  return api<CTFdChallenge>('GET', `${API_BASE}/ctfd/instances/${instanceId}/challenges/${challengeId}`)
}

export function submitCTFdFlag(instanceId: string, challengeId: number, flag: string) {
  return api<CTFdSubmitResponse>('POST', `${API_BASE}/ctfd/instances/${instanceId}/challenges/${challengeId}/submit`, { flag })
}

export function importCTFdChallenge(instanceId: string, challengeId: number) {
  return api<{ project: unknown; challenge: CTFdChallenge }>('POST', `${API_BASE}/ctfd/instances/${instanceId}/challenges/${challengeId}/import`)
}

export function ctfdFileUrl(instanceId: string, filePath: string) {
  return `${API_BASE}/ctfd/instances/${instanceId}/files/${filePath}`
}

// --- CTFd Challenge Instance (靶机) ---

export interface CTFdInstanceStatus {
  running: boolean
  instance?: {
    challenge_id: number
    ip: string
    port: number
    status: string
    lan_address: string
    message: string
  }
}

export function getCTFdInstanceStatus(instanceId: string, challengeId: number) {
  return api<CTFdInstanceStatus>('GET', `${API_BASE}/ctfd/instances/${instanceId}/challenges/${challengeId}/instance`)
}

export function startCTFdInstance(instanceId: string, challengeId: number) {
  return api<CTFdInstanceStatus>('POST', `${API_BASE}/ctfd/instances/${instanceId}/challenges/${challengeId}/instance/start`)
}

export function stopCTFdInstance(instanceId: string, challengeId: number) {
  return api<{ running: boolean }>('POST', `${API_BASE}/ctfd/instances/${instanceId}/challenges/${challengeId}/instance/stop`)
}

export function renewCTFdInstance(instanceId: string, challengeId: number) {
  return api<{ renewed: boolean }>('POST', `${API_BASE}/ctfd/instances/${instanceId}/challenges/${challengeId}/instance/renew`)
}

// --- Platform Agent ---

export interface AgentAction {
  type: string
  detail: string
  result: string
}

export interface AgentChatResponse {
  reply: string
  actions?: AgentAction[]
}

export interface AgentConfig {
  configured: boolean
}

export function agentChat(message: string) {
  return api<AgentChatResponse>('POST', `${API_BASE}/agent/chat`, { message })
}

export function getAgentConfig() {
  return api<AgentConfig>('GET', `${API_BASE}/agent/config`)
}

export function updateAgentConfig(config: { llm_base_url: string; llm_api_key: string; llm_model: string }) {
  return api<{ configured: boolean }>('PUT', `${API_BASE}/agent/config`, config)
}
