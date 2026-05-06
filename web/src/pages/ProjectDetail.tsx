import { useEffect, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useAppStore } from '../store'
import * as api from '../services/api'
import GraphView from '../components/GraphView'
import SidePanel from '../components/SidePanel'

export default function ProjectDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const project = useAppStore(s => s.project)
  const loadProject = useAppStore(s => s.loadProject)
  const loadProjects = useAppStore(s => s.loadProjects)
  const setSelectedProject = useAppStore(s => s.setSelectedProject)
  const setModal = useAppStore(s => s.setModal)
  const showToast = useAppStore(s => s.showToast)
  const selectedFacts = useAppStore(s => s.selectedFacts)
  const selectedNode = useAppStore(s => s.selectedNode)
  const isRefreshing = useAppStore(s => s.isRefreshing)
  const lastRefreshedAt = useAppStore(s => s.lastRefreshedAt)

  const refresh = useCallback(() => {
    if (id) loadProject(id)
  }, [id, loadProject])

  useEffect(() => {
    if (id) {
      setSelectedProject(id)
      loadProject(id)
    }
    const interval = setInterval(refresh, 3000)
    return () => clearInterval(interval)
  }, [id, loadProject, setSelectedProject, refresh])

  const handleToggleStop = async () => {
    if (!project) return
    const next = project.project.status === 'active' ? 'stopped' : 'active'
    try {
      await api.updateProjectStatus(project.project.id, next)
      await loadProjects()
      refresh()
      showToast(next === 'stopped' ? 'Project stopped' : 'Project resumed')
    } catch (e) { showToast((e as Error).message, 'error') }
  }

  if (!project) return <div className="flex-1 flex items-center justify-center text-slate-400">Loading...</div>

  const isActive = project.project.status === 'active'
  const canActOnFacts = isActive && selectedFacts.length > 0 && !selectedFacts.includes('goal')

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      {/* Header */}
      <header className="bg-white/80 backdrop-blur border-b border-slate-200/60 px-4 py-2.5 flex items-center gap-3 shrink-0 z-20">
        <button onClick={() => navigate('/')} className="p-1.5 rounded-lg hover:bg-slate-100 transition text-slate-400 hover:text-slate-600">
          <svg className="w-5 h-5" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="M15 19l-7-7 7-7" /></svg>
        </button>
        <div className="flex items-center gap-2 min-w-0">
          <span className="text-[11px] font-mono text-slate-400">{project.project.id.slice(0, 8)}</span>
          <h2 className="text-[15px] font-semibold text-slate-700 truncate">{project.project.title}</h2>
          <StatusBadge status={project.project.status} />
        </div>
        <div className="flex-1" />
        <div className="flex items-center gap-4 text-xs text-slate-400">
          <span>{(project.facts || []).length} facts</span>
          <span>{(project.intents || []).length} intents</span>
          <span className="flex items-center gap-1.5" title={lastRefreshedAt ? `Last sync: ${new Date(lastRefreshedAt).toLocaleTimeString()}` : ''}>
            <span className={`inline-block w-2 h-2 rounded-full transition-colors duration-300 ${isRefreshing ? 'bg-sky-400 animate-pulse' : 'bg-emerald-400'}`} />
            <span className="text-[10px]">{isRefreshing ? 'syncing' : 'live'}</span>
          </span>
        </div>
        <div className="flex items-center gap-1.5">
          {/* Graph actions */}
          {isActive && (
            <>
              <button onClick={() => setModal('showIntentModal', true)} disabled={!canActOnFacts}
                className="px-3 py-1.5 bg-white/90 backdrop-blur border border-indigo-200 text-indigo-600 rounded-lg shadow-sm text-xs font-medium hover:bg-indigo-50 transition disabled:opacity-30 disabled:cursor-not-allowed flex items-center gap-1">
                <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="M5 12h14" /><path d="m13 6 6 6-6 6" /></svg>
                Intent
              </button>
              <button onClick={() => setModal('showCompleteModal', true)} disabled={!canActOnFacts}
                className="px-3 py-1.5 bg-white/90 backdrop-blur border border-teal-200 text-teal-600 rounded-lg shadow-sm text-xs font-medium hover:bg-teal-50 transition disabled:opacity-30 disabled:cursor-not-allowed flex items-center gap-1">
                <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="M9 12.75L11.25 15 15 9.75M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" /></svg>
                Complete
              </button>
            </>
          )}
          {selectedNode?.type === 'intent' && isActive && (
            <button onClick={() => setModal('showConcludeModal', true)}
              className="px-3 py-1.5 bg-white/90 backdrop-blur border border-teal-200 text-teal-600 rounded-lg shadow-sm text-xs font-medium hover:bg-teal-50 transition flex items-center gap-1">
              Conclude
            </button>
          )}
          <button onClick={() => setModal('showHintModal', true)}
            className="px-3 py-1.5 bg-white/90 backdrop-blur border border-amber-200 text-amber-600 rounded-lg shadow-sm text-xs font-medium hover:bg-amber-50 transition flex items-center gap-1">
            <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="M9.663 17h4.673M12 3v1m6.364 1.636l-.707.707M21 12h-1M4 12H3" /></svg>
            Hint
          </button>
          {project.project.status === 'completed' && (
            <button onClick={() => setModal('showReopenModal', true)}
              className="px-3 py-1.5 bg-white/90 backdrop-blur border border-indigo-200 text-indigo-600 rounded-lg shadow-sm text-xs font-medium hover:bg-indigo-50 transition flex items-center gap-1">
              <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8" /><path d="M21 3v5h-5" /><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16" /><path d="M3 21v-5h5" /></svg>
              Reopen
            </button>
          )}
          <button onClick={() => setModal('showExportModal', true)}
            className="px-2.5 py-1 rounded-lg border border-slate-200 text-xs text-slate-500 hover:bg-slate-50 transition">
            Snapshot
          </button>
          <button onClick={() => setModal('showSettingsModal', true)}
            className="px-2 py-1 rounded-lg border border-slate-200 text-slate-400 hover:text-slate-600 hover:bg-slate-50 transition"
            title="Settings">
            <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth="1.7" viewBox="0 0 24 24">
              <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z" />
              <circle cx="12" cy="12" r="3" />
            </svg>
          </button>
          {project.project.status !== 'completed' && (
            <button onClick={handleToggleStop}
              className={`px-2.5 py-1 rounded-lg border text-xs transition ${
                isActive
                  ? 'border-amber-200 text-amber-600 hover:bg-amber-50'
                  : 'border-teal-200 text-teal-600 hover:bg-teal-50'
              }`}>
              {isActive ? 'Stop' : 'Resume'}
            </button>
          )}
          <button onClick={() => setModal('showDeleteModal', true)}
            className="px-2.5 py-1 rounded-lg border border-rose-200 text-xs text-rose-500 hover:bg-rose-50 transition">
            Delete
          </button>
        </div>
      </header>

      {/* Graph + Side Panel */}
      <div className="flex-1 flex overflow-hidden">
        <GraphView />
        <SidePanel />
      </div>
    </div>
  )
}

function StatusBadge({ status }: { status: string }) {
  const cls: Record<string, string> = {
    active: 'bg-teal-50 text-teal-600',
    stopped: 'bg-amber-50 text-amber-700',
    completed: 'bg-slate-100 text-slate-500',
  }
  return (
    <span className={`px-1.5 py-0.5 rounded-full text-[9px] font-semibold uppercase tracking-widest ${cls[status] || ''}`}>
      {status}
    </span>
  )
}
