import { useEffect, useState, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAppStore } from '../store'
import * as api from '../services/api'

export default function ProjectList() {
  const projects = useAppStore(s => s.projects)
  const loadProjects = useAppStore(s => s.loadProjects)
  const setModal = useAppStore(s => s.setModal)
  const showToast = useAppStore(s => s.showToast)
  const isRefreshing = useAppStore(s => s.isRefreshing)
  const navigate = useNavigate()

  const [statusFilter, setStatusFilter] = useState<string>('')
  const [searchQuery, setSearchQuery] = useState('')

  useEffect(() => {
    loadProjects()
    const interval = setInterval(loadProjects, 5000)
    return () => clearInterval(interval)
  }, [loadProjects])

  const filtered = useMemo(() => {
    return projects.filter(p => {
      if (statusFilter && p.status !== statusFilter) return false
      if (searchQuery) {
        const q = searchQuery.toLowerCase()
        if (!p.title.toLowerCase().includes(q) && !p.id.toLowerCase().includes(q)) return false
      }
      return true
    })
  }, [projects, statusFilter, searchQuery])

  const countByStatus = (status: string) => projects.filter(p => p.status === status).length

  const handleStop = async (id: string, e: React.MouseEvent) => {
    e.stopPropagation()
    try {
      await api.updateProjectStatus(id, 'stopped')
      await loadProjects()
      showToast('Project stopped')
    } catch (err) { showToast((err as Error).message, 'error') }
  }

  const handleResume = async (id: string, e: React.MouseEvent) => {
    e.stopPropagation()
    try {
      await api.updateProjectStatus(id, 'active')
      await loadProjects()
      showToast('Project resumed')
    } catch (err) { showToast((err as Error).message, 'error') }
  }

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      {/* Header */}
      <header className="bg-white/80 backdrop-blur border-b border-slate-200/60 px-4 py-2.5 flex items-center gap-3 shrink-0">
        <div className="flex items-center gap-2.5">
          <div className="h-8 w-8 rounded-lg border border-slate-200 bg-white shadow-sm flex items-center justify-center">
            <span className="text-indigo-600 font-bold text-sm">PT</span>
          </div>
          <span className="font-semibold text-slate-700 tracking-tight">PTAgent</span>
        </div>
        <div className="flex-1" />
        <div className="flex items-center gap-3 text-[11px] text-slate-400">
          {/* Status filter tabs */}
          <div className="flex items-center gap-1 bg-slate-100 rounded-lg p-0.5">
            <button onClick={() => setStatusFilter('')}
              className={`px-2 py-1 rounded-md transition text-[11px] font-medium ${
                statusFilter === '' ? 'bg-white text-slate-700 shadow-sm' : 'text-slate-400 hover:text-slate-600'
              }`}>
              All <span className="tabular-nums ml-0.5">{projects.length}</span>
            </button>
            <button onClick={() => setStatusFilter('active')}
              className={`px-2 py-1 rounded-md transition text-[11px] font-medium flex items-center gap-1 ${
                statusFilter === 'active' ? 'bg-white text-teal-700 shadow-sm' : 'text-slate-400 hover:text-teal-600'
              }`}>
              <span className="h-1.5 w-1.5 rounded-full bg-teal-500" />
              <span className="tabular-nums">{countByStatus('active')}</span>
            </button>
            <button onClick={() => setStatusFilter('stopped')}
              className={`px-2 py-1 rounded-md transition text-[11px] font-medium flex items-center gap-1 ${
                statusFilter === 'stopped' ? 'bg-white text-amber-700 shadow-sm' : 'text-slate-400 hover:text-amber-600'
              }`}>
              <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
              <span className="tabular-nums">{countByStatus('stopped')}</span>
            </button>
            <button onClick={() => setStatusFilter('completed')}
              className={`px-2 py-1 rounded-md transition text-[11px] font-medium flex items-center gap-1 ${
                statusFilter === 'completed' ? 'bg-white text-slate-600 shadow-sm' : 'text-slate-400 hover:text-slate-600'
              }`}>
              <span className="h-1.5 w-1.5 rounded-full bg-slate-400" />
              <span className="tabular-nums">{countByStatus('completed')}</span>
            </button>
          </div>

          {/* Search input */}
          <div className="relative">
            <svg className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-slate-300" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24">
              <circle cx="11" cy="11" r="8" /><path d="m21 21-4.35-4.35" />
            </svg>
            <input
              type="text"
              value={searchQuery}
              onChange={e => setSearchQuery(e.target.value)}
              placeholder="Search..."
              className="h-7 pl-8 pr-3 text-[11px] border border-slate-200 rounded-lg bg-white/80 focus:bg-white focus:outline-none focus:border-indigo-300 w-36 transition"
            />
          </div>

          <span className="flex items-center gap-1" title={isRefreshing ? 'Syncing...' : 'Live'}>
            <span className={`inline-block w-1.5 h-1.5 rounded-full transition-colors duration-300 ${isRefreshing ? 'bg-sky-400 animate-pulse' : 'bg-emerald-400'}`} />
          </span>
        </div>
        <button
          onClick={() => setModal('showDispatcherModal', true)}
          className="h-7 w-7 rounded-lg border border-slate-200 text-slate-400 hover:text-slate-600 hover:bg-slate-50 transition inline-flex items-center justify-center"
          title="Dispatchers"
        >
          <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth="1.7" viewBox="0 0 24 24">
            <path d="M21 12a9 9 0 0 0-9-9m9 9a9 9 0 0 1-9 9m9-9H3m9-9a9 9 0 0 0-9 9m9-9c1.66 0 3 4.03 3 9s-1.34 9-3 9m0-18c-1.66 0-3 4.03-3 9s1.34 9 3 9m-9-9a9 9 0 0 0 9 9" />
          </svg>
        </button>
        <button
          onClick={() => setModal('showSettingsModal', true)}
          className="h-7 w-7 rounded-lg border border-slate-200 text-slate-400 hover:text-slate-600 hover:bg-slate-50 transition inline-flex items-center justify-center"
          title="Settings"
        >
          <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth="1.7" viewBox="0 0 24 24">
            <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z" />
            <circle cx="12" cy="12" r="3" />
          </svg>
        </button>
        <button
          onClick={() => setModal('showNewProject', true)}
          className="h-7 px-2.5 rounded-lg border border-indigo-200 text-xs text-indigo-600 hover:bg-indigo-50 transition inline-flex items-center gap-1.5"
        >
          <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth="1.9" viewBox="0 0 24 24"><path d="M12 5v14" /><path d="M5 12h14" /></svg>
          New Project
        </button>
      </header>

      {/* Grid */}
      <div className="flex-1 overflow-y-auto p-4">
        {filtered.length === 0 && projects.length === 0 && (
          <div className="flex flex-col items-center justify-center h-full text-slate-400">
            <p className="text-lg font-medium text-slate-500">No projects yet</p>
            <p className="text-sm mt-1">Create a project to start exploring</p>
          </div>
        )}

        {filtered.length === 0 && projects.length > 0 && (
          <div className="flex flex-col items-center justify-center h-full text-slate-400">
            <p className="text-lg font-medium text-slate-500">No matching projects</p>
            <p className="text-sm mt-1">Try adjusting your search or filter</p>
          </div>
        )}

        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-3">
          {filtered.map(p => (
            <div
              key={p.id}
              onClick={() => navigate(`/projects/${p.id}`)}
              className="group h-full flex flex-col bg-white rounded-2xl border border-slate-200/60 p-5 cursor-pointer transition-all duration-200 hover:shadow-lg hover:shadow-slate-200/50 hover:border-slate-300 hover:-translate-y-0.5"
            >
              <div className="flex items-start justify-between mb-3">
                <span className="text-[11px] font-mono text-slate-400 tracking-wide">{p.id.slice(0, 8)}</span>
                <StatusBadge status={p.status} />
              </div>
              <div className="mb-3 flex-1">
                <h3 className="text-[15px] font-semibold text-slate-700 break-words group-hover:text-indigo-600 transition-colors">{p.title}</h3>
                {p.reason && (
                  <div className="mt-2 inline-flex items-center gap-1.5 rounded-full border border-sky-200 bg-sky-50 px-2 py-1 text-[10px] font-medium text-sky-700">
                    <span className="w-2 h-2 rounded-full bg-sky-400 animate-pulse" />
                    Reason · {p.reason.worker}
                  </div>
                )}
                {p.working_intent_count > 0 && (
                  <div className="mt-2 inline-flex items-center gap-1.5 rounded-full border border-amber-200 bg-amber-50 px-2 py-1 text-[10px] font-medium text-amber-700">
                    Exploring · {p.working_intent_count}
                  </div>
                )}
              </div>
              <div className="flex items-center gap-3 text-[11px] text-slate-400">
                <span className="flex items-center gap-1">
                  <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth="1.5" viewBox="0 0 24 24"><circle cx="12" cy="12" r="3" /><path d="M12 2v4m0 12v4m10-10h-4M6 12H2" /></svg>
                  {p.fact_count}
                </span>
                <span className="flex items-center gap-1">
                  <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth="1.5" viewBox="0 0 24 24"><path d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" /></svg>
                  {p.intent_count}
                </span>
                <span className="flex items-center gap-1">
                  <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth="1.5" viewBox="0 0 24 24"><path d="M9.663 17h4.673M12 3v1m6.364 1.636l-.707.707M21 12h-1M4 12H3" /></svg>
                  {p.hint_count}
                </span>
              </div>
              {/* Actions */}
              <div className="mt-3 pt-3 border-t border-slate-100 flex items-center justify-end gap-1.5">
                {p.status === 'active' && (
                  <button onClick={(e) => handleStop(p.id, e)}
                    className="px-2 py-1 rounded-lg border border-amber-200 text-[11px] text-amber-600 hover:bg-amber-50 transition">Stop</button>
                )}
                {p.status === 'stopped' && (
                  <button onClick={(e) => handleResume(p.id, e)}
                    className="px-2 py-1 rounded-lg border border-teal-200 text-[11px] text-teal-600 hover:bg-teal-50 transition">Resume</button>
                )}
                {p.status === 'completed' && (
                  <button onClick={(e) => { e.stopPropagation(); useAppStore.getState().setSelectedProject(p.id); setModal('showReopenModal', true) }}
                    className="px-2 py-1 rounded-lg border border-indigo-200 text-[11px] text-indigo-600 hover:bg-indigo-50 transition">Reopen</button>
                )}
              </div>
            </div>
          ))}
        </div>
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
