import { useState, useEffect } from 'react'
import { useAppStore } from '../store'
import * as api from '../services/api'
import type { DispatcherInstance } from '../services/api'

function formatUptime(startedAt: string | null): string {
  if (!startedAt) return '—'
  const diff = Date.now() - new Date(startedAt).getTime()
  const mins = Math.floor(diff / 60000)
  if (mins < 1) return '<1m'
  if (mins < 60) return `${mins}m`
  const hrs = Math.floor(mins / 60)
  return `${hrs}h ${mins % 60}m`
}

function StatusDot({ status }: { status: string }) {
  const cls: Record<string, string> = {
    running: 'bg-emerald-400',
    stopped: 'bg-slate-300',
    error: 'bg-rose-400',
  }
  return <span className={`inline-block w-2 h-2 rounded-full ${cls[status] || 'bg-slate-300'}`} />
}

function DispatcherCard({ inst, onRefresh }: { inst: DispatcherInstance; onRefresh: () => void }) {
  const showToast = useAppStore(s => s.showToast)

  const handleStop = async () => {
    try {
      await api.stopDispatcher(inst.id)
      onRefresh()
      showToast('Dispatcher stopped')
    } catch (e) { showToast((e as Error).message, 'error') }
  }

  const handleStart = async () => {
    try {
      await api.startDispatcher(inst.id)
      onRefresh()
      showToast('Dispatcher started')
    } catch (e) { showToast((e as Error).message, 'error') }
  }

  const handleDelete = async () => {
    try {
      await api.deleteDispatcher(inst.id)
      onRefresh()
      showToast('Dispatcher deleted')
    } catch (e) { showToast((e as Error).message, 'error') }
  }

  return (
    <div className="rounded-xl border border-slate-200 bg-white p-4 space-y-3">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <StatusDot status={inst.status} />
          <span className="text-sm font-semibold text-slate-700">{inst.name}</span>
          <span className="text-[10px] font-mono text-slate-400">{inst.id}</span>
        </div>
        <span className={`px-1.5 py-0.5 rounded-full text-[9px] font-semibold uppercase tracking-widest ${
          inst.status === 'running' ? 'bg-emerald-50 text-emerald-600' :
          inst.status === 'error' ? 'bg-rose-50 text-rose-600' :
          'bg-slate-100 text-slate-500'
        }`}>{inst.status}</span>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-4 gap-2 text-center">
        <div className="rounded-lg bg-slate-50 px-2 py-1.5">
          <div className="text-[10px] text-slate-400">Tasks</div>
          <div className="text-sm font-semibold text-slate-700">{inst.running_tasks}</div>
        </div>
        <div className="rounded-lg bg-slate-50 px-2 py-1.5">
          <div className="text-[10px] text-slate-400">Projects</div>
          <div className="text-sm font-semibold text-slate-700">{inst.admitted_count}</div>
        </div>
        <div className="rounded-lg bg-slate-50 px-2 py-1.5">
          <div className="text-[10px] text-slate-400">Max</div>
          <div className="text-sm font-semibold text-slate-700">{inst.runtime.max_workers}</div>
        </div>
        <div className="rounded-lg bg-slate-50 px-2 py-1.5">
          <div className="text-[10px] text-slate-400">Uptime</div>
          <div className="text-sm font-semibold text-slate-700">{formatUptime(inst.started_at)}</div>
        </div>
      </div>

      {/* Workers */}
      {inst.workers && inst.workers.length > 0 && (
        <div className="space-y-1.5">
          <div className="text-[10px] font-medium text-slate-400 uppercase tracking-wider">Workers</div>
          {inst.workers.map(w => (
            <div key={w.name} className="flex items-center justify-between text-xs px-2 py-1.5 rounded-lg bg-slate-50">
              <div className="flex items-center gap-2">
                <span className="font-medium text-slate-600">{w.name}</span>
                <span className="text-[10px] text-slate-400">{w.type}</span>
              </div>
              <div className="flex items-center gap-2">
                <span className="text-[10px] text-slate-400">{w.task_types.join(', ')}</span>
                <span className="font-mono text-slate-600">{w.running}/{w.max_running}</span>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Error */}
      {inst.error && (
        <div className="text-xs text-rose-600 bg-rose-50 rounded-lg px-3 py-2 break-words">{inst.error}</div>
      )}

      {/* Actions */}
      <div className="flex items-center justify-end gap-2 pt-2 border-t border-slate-100">
        {inst.status === 'running' && (
          <button onClick={handleStop} className="px-3 py-1.5 rounded-lg border border-amber-200 text-xs text-amber-600 hover:bg-amber-50 transition">Stop</button>
        )}
        {inst.status !== 'running' && (
          <button onClick={handleStart} className="px-3 py-1.5 rounded-lg border border-emerald-200 text-xs text-emerald-600 hover:bg-emerald-50 transition">Start</button>
        )}
        <button onClick={handleDelete} className="px-3 py-1.5 rounded-lg border border-rose-200 text-xs text-rose-500 hover:bg-rose-50 transition">Delete</button>
      </div>
    </div>
  )
}

function CreateForm({ onCreated }: { onCreated: () => void }) {
  const showToast = useAppStore(s => s.showToast)
  const [name, setName] = useState('dispatcher')
  const [configPath, setConfigPath] = useState('./configs/dispatch.yaml')
  const [creating, setCreating] = useState(false)

  const handleCreate = async () => {
    setCreating(true)
    try {
      await api.createDispatcher({ name, config_path: configPath })
      onCreated()
      showToast('Dispatcher created')
    } catch (e) { showToast((e as Error).message, 'error') }
    setCreating(false)
  }

  return (
    <div className="rounded-xl border border-dashed border-indigo-200 bg-indigo-50/30 p-4 space-y-3">
      <div className="text-xs font-medium text-indigo-600 uppercase tracking-wider">New Dispatcher</div>
      <div className="grid grid-cols-2 gap-2">
        <input value={name} onChange={e => setName(e.target.value)} placeholder="Name"
          className="px-3 py-2 border border-slate-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-100 focus:border-indigo-400" />
        <input value={configPath} onChange={e => setConfigPath(e.target.value)} placeholder="Config path"
          className="px-3 py-2 border border-slate-200 rounded-lg text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-100 focus:border-indigo-400" />
      </div>
      <div className="flex justify-end">
        <button onClick={handleCreate} disabled={creating || !name}
          className="px-4 py-2 text-xs bg-indigo-500 text-white rounded-lg font-medium hover:bg-indigo-600 disabled:opacity-30 shadow-sm transition">
          {creating ? 'Creating...' : 'Create & Start'}
        </button>
      </div>
    </div>
  )
}

export default function DispatcherModal() {
  const show = useAppStore(s => s.showDispatcherModal)
  const setModal = useAppStore(s => s.setModal)
  const dispatchers = useAppStore(s => s.dispatchers)
  const loadDispatchers = useAppStore(s => s.loadDispatchers)

  useEffect(() => {
    if (show) {
      loadDispatchers()
      const interval = setInterval(loadDispatchers, 3000)
      return () => clearInterval(interval)
    }
  }, [show, loadDispatchers])

  if (!show) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/20 backdrop-blur-sm modal-backdrop" onClick={() => setModal('showDispatcherModal', false)}>
      <div className="bg-white rounded-2xl shadow-2xl w-full max-w-2xl mx-4 border border-slate-200/60 overflow-hidden modal-content" onClick={e => e.stopPropagation()}>
        {/* Header */}
        <div className="px-5 py-4 border-b border-slate-200/80 flex items-center justify-between">
          <div className="flex items-center gap-2.5">
            <div className="h-7 w-7 rounded-lg bg-indigo-50 flex items-center justify-center">
              <svg className="w-4 h-4 text-indigo-600" fill="none" stroke="currentColor" strokeWidth="1.7" viewBox="0 0 24 24">
                <path d="M21 12a9 9 0 0 0-9-9m9 9a9 9 0 0 1-9 9m9-9H3m9-9a9 9 0 0 0-9 9m9-9c1.66 0 3 4.03 3 9s-1.34 9-3 9m0-18c-1.66 0-3 4.03-3 9s1.34 9 3 9m-9-9a9 9 0 0 0 9 9" />
              </svg>
            </div>
            <h3 className="text-base font-semibold text-slate-700">Dispatchers</h3>
            <span className="text-[11px] text-slate-400">{dispatchers.length} instance{dispatchers.length !== 1 ? 's' : ''}</span>
          </div>
          <button onClick={() => setModal('showDispatcherModal', false)} className="p-1.5 rounded-lg hover:bg-slate-100 transition text-slate-400 hover:text-slate-600">
            <svg className="w-5 h-5" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="M6 18L18 6M6 6l12 12" /></svg>
          </button>
        </div>

        {/* Content */}
        <div className="max-h-[70vh] overflow-y-auto p-5 space-y-4">
          {dispatchers.length === 0 && (
            <div className="text-center py-8 text-slate-400">
              <p className="text-sm">No dispatchers running</p>
              <p className="text-xs mt-1">Create one below to start scheduling</p>
            </div>
          )}
          {dispatchers.map(d => (
            <DispatcherCard key={d.id} inst={d} onRefresh={loadDispatchers} />
          ))}
          <CreateForm onCreated={loadDispatchers} />
        </div>
      </div>
    </div>
  )
}
