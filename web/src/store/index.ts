import { create } from 'zustand'
import type { ProjectSummary, ProjectDetail, Settings, DispatcherInstance, TaskEvent, SSEEvent } from '../services/api'
import * as api from '../services/api'

export type LayoutMode = 'dagre_tb' | 'dagre_lr' | 'klay_tb' | 'klay_lr' | 'elk_tb' | 'elk_lr'

export interface SelectedNode {
  type: 'fact' | 'intent'
  id: string
}

interface AppState {
  // Projects
  projects: ProjectSummary[]
  project: ProjectDetail | null
  selectedProjectId: string

  // Selection
  selectedNode: SelectedNode | null
  selectedFacts: string[]
  sideTab: 'detail' | 'hints' | 'log' | 'replay' | 'live'

  // Layout
  layoutMode: LayoutMode
  sidePanelWidth: number

  // Modals
  showNewProject: boolean
  showIntentModal: boolean
  showConcludeModal: boolean
  showCompleteModal: boolean
  showHintModal: boolean
  showDeleteModal: boolean
  showSettingsModal: boolean
  showExportModal: boolean
  showReopenModal: boolean
  showDispatcherModal: boolean

  // Refresh
  isRefreshing: boolean
  lastRefreshedAt: number | null

  // Dispatchers
  dispatchers: DispatcherInstance[]

  // Settings
  settings: Settings

  // Task Events (replay)
  taskEvents: TaskEvent[]

  // Live Progress (SSE)
  liveEvents: SSEEvent[]
  sseConnected: boolean

  // Toast
  toast: { show: boolean; message: string; type: 'info' | 'error' }

  // Actor
  actorName: string

  // Actions
  loadProjects: () => Promise<void>
  loadProject: (id: string) => Promise<void>
  setSelectedProject: (id: string) => void
  setSelectedNode: (node: SelectedNode | null) => void
  setSelectedFacts: (facts: string[]) => void
  toggleFactSelection: (fid: string) => void
  clearSelection: () => void
  setSideTab: (tab: 'detail' | 'hints' | 'log' | 'replay' | 'live') => void
  setLayoutMode: (mode: LayoutMode) => void
  setSidePanelWidth: (width: number) => void
  showToast: (message: string, type?: 'info' | 'error') => void
  setModal: (modal: string, show: boolean) => void
  setActorName: (name: string) => void
  loadSettings: () => Promise<void>
  loadDispatchers: () => Promise<void>
  loadTaskEvents: (projectId: string) => Promise<void>
  connectSSE: () => void
  disconnectSSE: () => void
}

export const useAppStore = create<AppState>((set, get) => ({
  projects: [],
  project: null,
  selectedProjectId: '',
  selectedNode: null,
  selectedFacts: [],
  sideTab: 'detail',
  layoutMode: (localStorage.getItem('ptagent.layoutMode') as LayoutMode) || 'dagre_tb',
  sidePanelWidth: Number(localStorage.getItem('ptagent.sidePanelWidth')) || 320,
  showNewProject: false,
  showIntentModal: false,
  showConcludeModal: false,
  showCompleteModal: false,
  showHintModal: false,
  showDeleteModal: false,
  showSettingsModal: false,
  showExportModal: false,
  showReopenModal: false,
  showDispatcherModal: false,
  isRefreshing: false,
  lastRefreshedAt: null,
  dispatchers: [],
  settings: { intent_timeout: 5, reason_timeout: 5 },
  taskEvents: [],
  liveEvents: [],
  sseConnected: false,
  toast: { show: false, message: '', type: 'info' },
  actorName: localStorage.getItem('ptagent.actorName') || 'Human',

  loadProjects: async () => {
    set({ isRefreshing: true })
    try {
      const projects = await api.listProjects()
      set({ projects: projects || [], lastRefreshedAt: Date.now() })
    } catch (e) {
      console.error(e)
    } finally {
      set({ isRefreshing: false })
    }
  },

  loadProject: async (id: string) => {
    set({ isRefreshing: true })
    try {
      const next = await api.getProject(id)
      if (next) {
        next.facts = next.facts || []
        next.intents = next.intents || []
        next.hints = next.hints || []
      }
      // Only update state if data actually changed to avoid unnecessary re-renders
      const prev = get().project
      if (JSON.stringify(prev) !== JSON.stringify(next)) {
        set({ project: next })
      }
      set({ lastRefreshedAt: Date.now() })
    } catch (e) {
      get().showToast((e as Error).message, 'error')
    } finally {
      set({ isRefreshing: false })
    }
  },

  setSelectedProject: (id) => set({ selectedProjectId: id }),

  setSelectedNode: (node) => set({ selectedNode: node }),

  setSelectedFacts: (facts) => set({ selectedFacts: facts }),

  toggleFactSelection: (fid) => {
    const { selectedFacts } = get()
    const idx = selectedFacts.indexOf(fid)
    if (idx >= 0) {
      const next = selectedFacts.filter(f => f !== fid)
      set({
        selectedFacts: next,
        selectedNode: next.length > 0 ? { type: 'fact', id: next[next.length - 1] } : null,
      })
    } else {
      set({
        selectedFacts: [...selectedFacts, fid],
        selectedNode: { type: 'fact', id: fid },
      })
    }
  },

  clearSelection: () => set({ selectedNode: null, selectedFacts: [] }),

  setSideTab: (tab) => set({ sideTab: tab }),

  setLayoutMode: (mode) => {
    localStorage.setItem('ptagent.layoutMode', mode)
    set({ layoutMode: mode })
  },

  setSidePanelWidth: (width) => {
    localStorage.setItem('ptagent.sidePanelWidth', String(width))
    set({ sidePanelWidth: width })
  },

  showToast: (message, type = 'info') => {
    // Clear any pending auto-hide timer
    const prevTimer = (window as any).__ptagent_toast_timer as ReturnType<typeof setTimeout> | undefined
    if (prevTimer) clearTimeout(prevTimer)
    set({ toast: { show: true, message, type } })
    const timer = setTimeout(() => set({ toast: { show: false, message: '', type: 'info' } }), 3000)
    ;(window as any).__ptagent_toast_timer = timer
  },

  setModal: (modal, show) => set({ [modal]: show } as Partial<AppState>),

  setActorName: (name) => {
    localStorage.setItem('ptagent.actorName', name)
    set({ actorName: name })
  },

  loadSettings: async () => {
    try {
      const settings = await api.getSettings()
      set({ settings })
    } catch (e) {
      console.error(e)
    }
  },

  loadDispatchers: async () => {
    try {
      const dispatchers = await api.listDispatchers()
      set({ dispatchers: dispatchers || [] })
    } catch (e) {
      console.error(e)
    }
  },

  loadTaskEvents: async (projectId: string) => {
    try {
      const events = await api.listTaskEvents(projectId, { limit: 200 })
      set({ taskEvents: events || [] })
    } catch (e) {
      console.error(e)
    }
  },

  connectSSE: () => {
    const state = get()
    if (state.sseConnected) return

    // Close any lingering EventSource to prevent duplicates
    const prev = (window as any).__ptagent_sse as EventSource | undefined
    if (prev) {
      prev.close()
      delete (window as any).__ptagent_sse
    }

    const es = api.connectSSE((event: SSEEvent) => {
      const { liveEvents, selectedProjectId, loadProject } = get()
      // Keep last 50 events
      const next = [...liveEvents, event].slice(-50)
      set({ liveEvents: next })

      // Auto-refresh project if the event is for the currently viewed project
      if (event.project_id && event.project_id === selectedProjectId) {
        if (['intent_created', 'intent_concluded', 'fact_created', 'task_completed', 'task_failed'].includes(event.type)) {
          loadProject(selectedProjectId)
        }
      }
    })

    es.onerror = () => {
      set({ sseConnected: false })
      // Auto-reconnect after 3s
      setTimeout(() => {
        const { sseConnected } = get()
        if (!sseConnected) get().connectSSE()
      }, 3000)
    }

    es.onopen = () => set({ sseConnected: true })

    // Store EventSource ref for cleanup
    ;(window as any).__ptagent_sse = es
    set({ sseConnected: true })
  },

  disconnectSSE: () => {
    const es = (window as any).__ptagent_sse as EventSource | undefined
    if (es) {
      es.close()
      delete (window as any).__ptagent_sse
    }
    set({ sseConnected: false })
  },
}))
