import { useState, useEffect, useCallback } from 'react'
import * as api from '../services/api'
import type { CTFdInstance, CTFdChallenge, CTFdInstanceStatus } from '../services/api'
import { useAppStore } from '../store'

type View = 'instances' | 'challenges' | 'detail'

export default function CTFdPanel() {
  const [open, setOpen] = useState(false)
  const [view, setView] = useState<View>('instances')
  const [instances, setInstances] = useState<CTFdInstance[]>([])
  const [selectedInstance, setSelectedInstance] = useState<CTFdInstance | null>(null)
  const [challenges, setChallenges] = useState<CTFdChallenge[]>([])
  const [selectedChallenge, setSelectedChallenge] = useState<CTFdChallenge | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [filter, setFilter] = useState('')
  const [categoryFilter, setCategoryFilter] = useState('')

  // Add instance form
  const [showAddForm, setShowAddForm] = useState(false)
  const [addForm, setAddForm] = useState({ name: '', url: '', token: '' })

  // Flag submit
  const [flagInput, setFlagInput] = useState('')
  const [submitResult, setSubmitResult] = useState<{ status: string; message: string } | null>(null)

  // Challenge instance (靶机)
  const [instanceStatus, setInstanceStatus] = useState<CTFdInstanceStatus | null>(null)
  const [instanceLoading, setInstanceLoading] = useState(false)

  const showToast = useAppStore(s => s.showToast)
  const loadProjects = useAppStore(s => s.loadProjects)

  const loadInstances = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const data = await api.listCTFdInstances()
      setInstances(data || [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (open && view === 'instances') loadInstances()
  }, [open, view, loadInstances])

  const handleAddInstance = async () => {
    if (!addForm.name || !addForm.url || !addForm.token) return
    setLoading(true)
    setError('')
    try {
      await api.addCTFdInstance(addForm)
      setAddForm({ name: '', url: '', token: '' })
      setShowAddForm(false)
      await loadInstances()
      showToast('CTFd instance added')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  const handleDeleteInstance = async (id: string) => {
    try {
      await api.deleteCTFdInstance(id)
      await loadInstances()
      showToast('Instance removed')
    } catch (e) {
      setError((e as Error).message)
    }
  }

  const handleSelectInstance = async (inst: CTFdInstance) => {
    setSelectedInstance(inst)
    setView('challenges')
    setLoading(true)
    setError('')
    try {
      const data = await api.listCTFdChallenges(inst.id)
      setChallenges(data || [])
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  const handleSelectChallenge = async (ch: CTFdChallenge) => {
    setSelectedChallenge(ch)
    setView('detail')
    setFlagInput('')
    setSubmitResult(null)
    setInstanceStatus(null)
    // Fetch detail with files
    if (selectedInstance) {
      try {
        const detail = await api.getCTFdChallenge(selectedInstance.id, ch.id)
        setSelectedChallenge(detail)
      } catch { /* use basic info */ }
      // Check if this challenge type supports instances (靶机)
      loadInstanceStatus(selectedInstance.id, ch.id)
    }
  }

  const loadInstanceStatus = async (instId: string, challId: number) => {
    try {
      const status = await api.getCTFdInstanceStatus(instId, challId)
      setInstanceStatus(status)
    } catch {
      // Instance API not available (no CTFd-Whale plugin) - that's OK
      setInstanceStatus(null)
    }
  }

  const handleStartInstance = async () => {
    if (!selectedInstance || !selectedChallenge) return
    setInstanceLoading(true)
    try {
      const result = await api.startCTFdInstance(selectedInstance.id, selectedChallenge.id)
      setInstanceStatus(result)
      showToast('Instance starting...')
      // Poll status after a short delay (container creation takes time)
      setTimeout(() => loadInstanceStatus(selectedInstance.id, selectedChallenge.id), 3000)
    } catch (e) {
      showToast((e as Error).message, 'error')
    } finally {
      setInstanceLoading(false)
    }
  }

  const handleStopInstance = async () => {
    if (!selectedInstance || !selectedChallenge) return
    setInstanceLoading(true)
    try {
      await api.stopCTFdInstance(selectedInstance.id, selectedChallenge.id)
      setInstanceStatus({ running: false })
      showToast('Instance stopped')
    } catch (e) {
      showToast((e as Error).message, 'error')
    } finally {
      setInstanceLoading(false)
    }
  }

  const handleRenewInstance = async () => {
    if (!selectedInstance || !selectedChallenge) return
    setInstanceLoading(true)
    try {
      await api.renewCTFdInstance(selectedInstance.id, selectedChallenge.id)
      showToast('Instance renewed')
      await loadInstanceStatus(selectedInstance.id, selectedChallenge.id)
    } catch (e) {
      showToast((e as Error).message, 'error')
    } finally {
      setInstanceLoading(false)
    }
  }

  const handleSubmitFlag = async () => {
    if (!selectedInstance || !selectedChallenge || !flagInput) return
    setLoading(true)
    try {
      const result = await api.submitCTFdFlag(selectedInstance.id, selectedChallenge.id, flagInput)
      setSubmitResult(result)
      if (result.status === 'correct') {
        showToast('Flag correct!', 'info')
      }
    } catch (e) {
      setSubmitResult({ status: 'error', message: (e as Error).message })
    } finally {
      setLoading(false)
    }
  }

  const handleImport = async (ch: CTFdChallenge) => {
    if (!selectedInstance) return
    setLoading(true)
    try {
      await api.importCTFdChallenge(selectedInstance.id, ch.id)
      showToast(`Imported: ${ch.name}`)
      await loadProjects()
    } catch (e) {
      showToast((e as Error).message, 'error')
    } finally {
      setLoading(false)
    }
  }

  // Categories
  const categories = [...new Set(challenges.map(c => c.category))].sort()
  const filtered = challenges.filter(ch => {
    if (categoryFilter && ch.category !== categoryFilter) return false
    if (filter && !ch.name.toLowerCase().includes(filter.toLowerCase()) &&
        !ch.category.toLowerCase().includes(filter.toLowerCase())) return false
    return true
  })

  return (
    <>
      {/* Floating Toggle Button */}
      <button
        onClick={() => setOpen(!open)}
        className={`fixed bottom-6 right-6 z-50 w-14 h-14 rounded-full shadow-lg flex items-center justify-center transition-all duration-300 ${
          open ? 'bg-slate-700 text-white rotate-45' : 'bg-indigo-600 text-white hover:bg-indigo-700 hover:scale-105'
        }`}
        title="CTFd Agent"
      >
        {open ? (
          <svg className="w-6 h-6" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="M12 5v14m-7-7h14" /></svg>
        ) : (
          <svg className="w-6 h-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
            <path d="M3 21V3h18v18H3z" /><path d="M3 9h18M9 3v18" />
            <circle cx="15" cy="15" r="2.5" />
          </svg>
        )}
      </button>

      {/* Floating Panel */}
      {open && (
        <div className="fixed bottom-24 right-6 z-50 w-[480px] max-h-[calc(100vh-8rem)] bg-white rounded-2xl shadow-2xl border border-slate-200/60 flex flex-col overflow-hidden animate-in slide-in-from-bottom-4 duration-200">
          {/* Header */}
          <div className="px-5 py-3.5 border-b border-slate-100 bg-gradient-to-r from-indigo-50 to-violet-50 shrink-0">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2.5">
                <div className="w-8 h-8 rounded-lg bg-indigo-600 flex items-center justify-center">
                  <svg className="w-4.5 h-4.5 text-white" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                    <path d="M3 21V3h18v18H3z" /><path d="M3 9h18M9 3v18" />
                  </svg>
                </div>
                <div>
                  <h3 className="text-sm font-semibold text-slate-800">CTFd Agent</h3>
                  <p className="text-[10px] text-slate-400">
                    {view === 'instances' ? 'Platforms' :
                     view === 'challenges' ? selectedInstance?.name :
                     selectedChallenge?.name}
                  </p>
                </div>
              </div>
              {/* Solved/Total stats badge */}
              {view !== 'instances' && challenges.length > 0 && (
                <div className="flex items-center gap-1.5 mr-2">
                  <div className="flex items-center gap-1 px-2 py-1 bg-emerald-50 rounded-lg">
                    <span className="text-[11px] font-bold text-emerald-600">{challenges.filter(c => c.solved).length}</span>
                    <span className="text-[10px] text-slate-400">/</span>
                    <span className="text-[11px] font-medium text-slate-500">{challenges.length}</span>
                  </div>
                </div>
              )}
              {/* Breadcrumb nav */}
              <div className="flex items-center gap-1 text-[11px]">
                <button onClick={() => { setView('instances'); setCategoryFilter(''); setFilter('') }}
                  className={`px-2 py-0.5 rounded ${view === 'instances' ? 'bg-indigo-100 text-indigo-700 font-medium' : 'text-slate-400 hover:text-slate-600'}`}>
                  Platforms
                </button>
                {view !== 'instances' && (
                  <>
                    <span className="text-slate-300">/</span>
                    <button onClick={() => { setView('challenges'); setFilter(''); setCategoryFilter('') }}
                      className={`px-2 py-0.5 rounded truncate max-w-[80px] ${view === 'challenges' ? 'bg-indigo-100 text-indigo-700 font-medium' : 'text-slate-400 hover:text-slate-600'}`}>
                      Challs
                    </button>
                  </>
                )}
                {view === 'detail' && (
                  <>
                    <span className="text-slate-300">/</span>
                    <span className="px-2 py-0.5 bg-indigo-100 text-indigo-700 font-medium rounded truncate max-w-[80px]">
                      Detail
                    </span>
                  </>
                )}
              </div>
            </div>
          </div>

          {/* Error */}
          {error && (
            <div className="mx-4 mt-3 px-3 py-2 bg-red-50 border border-red-200 rounded-lg text-xs text-red-600">
              {error}
              <button onClick={() => setError('')} className="ml-2 text-red-400 hover:text-red-600">×</button>
            </div>
          )}

          {/* Content */}
          <div className="flex-1 overflow-y-auto p-4">
            {view === 'instances' && <InstancesView
              instances={instances}
              loading={loading}
              showAddForm={showAddForm}
              addForm={addForm}
              onToggleAdd={() => setShowAddForm(!showAddForm)}
              onUpdateForm={setAddForm}
              onAdd={handleAddInstance}
              onDelete={handleDeleteInstance}
              onSelect={handleSelectInstance}
            />}

            {view === 'challenges' && <ChallengesView
              challenges={filtered}
              categories={categories}
              filter={filter}
              categoryFilter={categoryFilter}
              loading={loading}
              onFilterChange={setFilter}
              onCategoryChange={setCategoryFilter}
              onSelect={handleSelectChallenge}
              onImport={handleImport}
            />}

            {view === 'detail' && selectedChallenge && selectedInstance && <ChallengeDetail
              challenge={selectedChallenge}
              instance={selectedInstance}
              flagInput={flagInput}
              submitResult={submitResult}
              loading={loading}
              onFlagChange={setFlagInput}
              onSubmit={handleSubmitFlag}
              onImport={() => handleImport(selectedChallenge)}
              instanceStatus={instanceStatus}
              instanceLoading={instanceLoading}
              onStartInstance={handleStartInstance}
              onStopInstance={handleStopInstance}
              onRenewInstance={handleRenewInstance}
            />}
          </div>
        </div>
      )}
    </>
  )
}

// --- Sub-views ---

function InstancesView({ instances, loading, showAddForm, addForm, onToggleAdd, onUpdateForm, onAdd, onDelete, onSelect }: {
  instances: CTFdInstance[]
  loading: boolean
  showAddForm: boolean
  addForm: { name: string; url: string; token: string }
  onToggleAdd: () => void
  onUpdateForm: (form: { name: string; url: string; token: string }) => void
  onAdd: () => void
  onDelete: (id: string) => void
  onSelect: (inst: CTFdInstance) => void
}) {
  return (
    <div className="space-y-3">
      <button onClick={onToggleAdd}
        className="w-full px-3 py-2 border-2 border-dashed border-slate-200 rounded-xl text-xs text-slate-400 hover:border-indigo-300 hover:text-indigo-500 transition flex items-center justify-center gap-1.5">
        <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="M12 5v14m-7-7h14" /></svg>
        Add CTFd Platform
      </button>

      {showAddForm && (
        <div className="rounded-xl border border-indigo-200 bg-indigo-50/50 p-3 space-y-2">
          <input value={addForm.name} onChange={e => onUpdateForm({ ...addForm, name: e.target.value })}
            placeholder="Name (e.g. HackTheBox)"
            className="w-full px-3 py-1.5 text-xs border border-slate-200 rounded-lg bg-white" />
          <input value={addForm.url} onChange={e => onUpdateForm({ ...addForm, url: e.target.value })}
            placeholder="URL (e.g. https://ctfd.example.com)"
            className="w-full px-3 py-1.5 text-xs border border-slate-200 rounded-lg bg-white" />
          <input value={addForm.token} onChange={e => onUpdateForm({ ...addForm, token: e.target.value })}
            placeholder="API Token" type="password"
            className="w-full px-3 py-1.5 text-xs border border-slate-200 rounded-lg bg-white" />
          <div className="flex gap-2">
            <button onClick={onAdd} disabled={loading}
              className="flex-1 px-3 py-1.5 bg-indigo-600 text-white rounded-lg text-xs font-medium hover:bg-indigo-700 disabled:opacity-50 transition">
              {loading ? 'Connecting...' : 'Connect'}
            </button>
            <button onClick={onToggleAdd}
              className="px-3 py-1.5 border border-slate-200 rounded-lg text-xs text-slate-500 hover:bg-slate-50 transition">
              Cancel
            </button>
          </div>
        </div>
      )}

      {instances.length === 0 && !showAddForm && (
        <div className="text-center py-8 text-slate-300 text-sm">No platforms connected</div>
      )}

      {instances.map(inst => (
        <div key={inst.id}
          className="rounded-xl border border-slate-200 bg-white shadow-sm hover:shadow-md transition p-3 cursor-pointer group"
          onClick={() => onSelect(inst)}>
          <div className="flex items-center justify-between">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <span className="w-2 h-2 rounded-full bg-emerald-400" />
                <span className="text-sm font-medium text-slate-700 truncate">{inst.name}</span>
              </div>
              <p className="text-[11px] text-slate-400 mt-0.5 truncate">{inst.url}</p>
            </div>
            <div className="flex items-center gap-1.5 opacity-0 group-hover:opacity-100 transition">
              <button onClick={(e) => { e.stopPropagation(); onDelete(inst.id) }}
                className="p-1 rounded hover:bg-red-50 text-slate-300 hover:text-red-500 transition">
                <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="m19 7-.867 12.142A2 2 0 0 1 16.138 21H7.862a2 2 0 0 1-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 0 0-1-1h-4a1 1 0 0 0-1 1v3M4 7h16" /></svg>
              </button>
              <svg className="w-4 h-4 text-slate-300 group-hover:text-indigo-400 transition" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="m9 5 7 7-7 7" /></svg>
            </div>
          </div>
        </div>
      ))}
    </div>
  )
}

function ChallengesView({ challenges, categories, filter, categoryFilter, loading, onFilterChange, onCategoryChange, onSelect, onImport }: {
  challenges: CTFdChallenge[]
  categories: string[]
  filter: string
  categoryFilter: string
  loading: boolean
  onFilterChange: (v: string) => void
  onCategoryChange: (v: string) => void
  onSelect: (ch: CTFdChallenge) => void
  onImport: (ch: CTFdChallenge) => void
}) {
  if (loading) return <div className="text-center py-8 text-slate-300 text-sm">Loading challenges...</div>

  return (
    <div className="space-y-3">
      {/* Filters */}
      <div className="flex gap-2">
        <input value={filter} onChange={e => onFilterChange(e.target.value)}
          placeholder="Search..."
          className="flex-1 px-3 py-1.5 text-xs border border-slate-200 rounded-lg bg-white" />
        <select value={categoryFilter} onChange={e => onCategoryChange(e.target.value)}
          className="px-2 py-1.5 text-xs border border-slate-200 rounded-lg bg-white text-slate-600 max-w-[120px]">
          <option value="">All</option>
          {categories.map(cat => <option key={cat} value={cat}>{cat}</option>)}
        </select>
      </div>

      {/* Stats bar */}
      <div className="flex items-center gap-3 text-[11px] text-slate-400">
        <span>{challenges.length} challenges</span>
        <span className="text-emerald-500 font-medium">{challenges.filter(c => c.solved).length} solved</span>
        <span>{challenges.reduce((s, c) => s + (c.solved ? c.value : 0), 0)} / {challenges.reduce((s, c) => s + c.value, 0)} pts</span>
      </div>

      {/* Challenge list */}
      {challenges.map(ch => (
        <div key={ch.id}
          className="rounded-xl border border-slate-200 bg-white shadow-sm hover:shadow-md transition p-3 cursor-pointer group"
          onClick={() => onSelect(ch)}>
          <div className="flex items-start justify-between gap-2">
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="text-sm font-medium text-slate-700">{ch.name}</span>
                {ch.solved && (
                  <span className="px-1.5 py-0.5 bg-emerald-50 text-emerald-600 text-[10px] font-semibold rounded">SOLVED</span>
                )}
              </div>
              <div className="flex items-center gap-2 mt-1 text-[11px] text-slate-400">
                <span className="px-1.5 py-0.5 bg-slate-100 rounded text-slate-500">{ch.category}</span>
                <span>{ch.value} pts</span>
                <span>{ch.solves} solves</span>
                {ch.max_attempts > 0 && (
                  <span className={`px-1.5 py-0.5 rounded text-[10px] font-medium ${
                    ch.attempts >= ch.max_attempts ? 'bg-red-50 text-red-500' : 'bg-amber-50 text-amber-600'
                  }`}>{ch.attempts}/{ch.max_attempts} att</span>
                )}
              </div>
            </div>
            <div className="flex items-center gap-1 shrink-0 opacity-0 group-hover:opacity-100 transition">
              <button onClick={(e) => { e.stopPropagation(); onImport(ch) }}
                title="Import as project"
                className="p-1.5 rounded-lg hover:bg-indigo-50 text-slate-300 hover:text-indigo-600 transition">
                <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="M12 5v14m-7-7h14" /></svg>
              </button>
            </div>
          </div>
        </div>
      ))}

      {challenges.length === 0 && (
        <div className="text-center py-8 text-slate-300 text-sm">No challenges found</div>
      )}
    </div>
  )
}

function ChallengeDetail({ challenge, instance, flagInput, submitResult, loading, onFlagChange, onSubmit, onImport, instanceStatus, instanceLoading, onStartInstance, onStopInstance, onRenewInstance }: {
  challenge: CTFdChallenge
  instance: CTFdInstance
  flagInput: string
  submitResult: { status: string; message: string } | null
  loading: boolean
  onFlagChange: (v: string) => void
  onSubmit: () => void
  onImport: () => void
  instanceStatus: CTFdInstanceStatus | null
  instanceLoading: boolean
  onStartInstance: () => void
  onStopInstance: () => void
  onRenewInstance: () => void
}) {
  return (
    <div className="space-y-4">
      {/* Header */}
      <div>
        <div className="flex items-center gap-2 flex-wrap">
          <h4 className="text-base font-semibold text-slate-800">{challenge.name}</h4>
          {challenge.solved && (
            <span className="px-2 py-0.5 bg-emerald-50 text-emerald-600 text-[10px] font-semibold rounded-full">SOLVED</span>
          )}
        </div>
        <div className="flex items-center gap-3 mt-1 text-[11px] text-slate-400">
          <span className="px-1.5 py-0.5 bg-indigo-50 text-indigo-600 rounded font-medium">{challenge.category}</span>
          <span>{challenge.value} pts</span>
          <span>{challenge.solves} solves</span>
          <span className="text-slate-300">{challenge.type}</span>
          {challenge.max_attempts > 0 && (
            <span className={`px-1.5 py-0.5 rounded font-medium ${
              challenge.attempts >= challenge.max_attempts ? 'bg-red-50 text-red-500' : 'bg-amber-50 text-amber-600'
            }`}>{challenge.attempts}/{challenge.max_attempts} attempts</span>
          )}
        </div>
      </div>

      {/* Description */}
      <div className="rounded-xl border border-slate-200 bg-slate-50/80 p-3">
        <div className="text-xs text-slate-600 leading-relaxed whitespace-pre-wrap break-words"
          dangerouslySetInnerHTML={{ __html: challenge.description }} />
      </div>

      {/* Connection info */}
      {challenge.connection_info && (
        <div className="rounded-lg border border-blue-200 bg-blue-50/80 px-3 py-2">
          <span className="text-[10px] text-blue-500 font-medium uppercase tracking-wider">Connection</span>
          <p className="text-xs text-blue-700 font-mono mt-0.5">{challenge.connection_info}</p>
        </div>
      )}

      {/* Challenge Instance (靶机) Controls */}
      {instanceStatus !== null && (
        <div className="rounded-xl border border-slate-200 bg-slate-50/80 p-3 space-y-2">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <span className={`w-2 h-2 rounded-full ${instanceStatus.running ? 'bg-emerald-400 animate-pulse' : 'bg-slate-300'}`} />
              <span className="text-[11px] font-medium text-slate-600">
                {instanceStatus.running ? 'Instance Running' : 'Instance Stopped'}
              </span>
            </div>
            <div className="flex items-center gap-1.5">
              {!instanceStatus.running ? (
                <button onClick={onStartInstance} disabled={instanceLoading}
                  className="px-3 py-1 bg-emerald-600 text-white rounded-lg text-[11px] font-medium hover:bg-emerald-700 disabled:opacity-50 transition">
                  {instanceLoading ? 'Starting...' : 'Start'}
                </button>
              ) : (
                <>
                  <button onClick={onRenewInstance} disabled={instanceLoading}
                    className="px-2.5 py-1 bg-blue-500 text-white rounded-lg text-[11px] font-medium hover:bg-blue-600 disabled:opacity-50 transition">
                    Renew
                  </button>
                  <button onClick={onStopInstance} disabled={instanceLoading}
                    className="px-2.5 py-1 bg-red-500 text-white rounded-lg text-[11px] font-medium hover:bg-red-600 disabled:opacity-50 transition">
                    Stop
                  </button>
                </>
              )}
            </div>
          </div>
          {instanceStatus.running && instanceStatus.instance && (
            <div className="text-[11px] text-slate-500 font-mono bg-white rounded-lg px-2.5 py-1.5 border border-slate-100">
              {instanceStatus.instance.lan_address || `${instanceStatus.instance.ip}:${instanceStatus.instance.port}`}
            </div>
          )}
          {instanceStatus.running && (
            <p className="text-[10px] text-amber-600">
              ⚠ Only one instance can run at a time. Stop before starting another challenge.
            </p>
          )}
        </div>
      )}

      {/* Tags */}
      {challenge.tags && challenge.tags.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {challenge.tags.map((tag, i) => (
            <span key={i} className="px-2 py-0.5 bg-violet-50 text-violet-600 text-[10px] rounded-full">{tag}</span>
          ))}
        </div>
      )}

      {/* Files / Attachments */}
      {challenge.files && challenge.files.length > 0 && (
        <div className="space-y-1.5">
          <span className="text-[11px] text-slate-500 font-medium">Attachments</span>
          {challenge.files.map(f => {
            const filename = f.location.split('/').pop() || f.location
            const downloadUrl = api.ctfdFileUrl(instance.id, f.location)
            return (
              <a key={f.id || f.location} href={downloadUrl} download
                className="flex items-center gap-2 px-3 py-2 rounded-lg border border-slate-200 bg-white hover:bg-slate-50 transition group">
                <svg className="w-4 h-4 text-slate-300 group-hover:text-indigo-500 transition" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3" />
                </svg>
                <span className="text-xs text-slate-600 font-mono truncate">{filename}</span>
              </a>
            )
          })}
        </div>
      )}

      {/* Flag submit */}
      <div className="space-y-2">
        <span className="text-[11px] text-slate-500 font-medium">Submit Flag</span>
        {challenge.max_attempts > 0 && (
          <div className={`px-3 py-2 rounded-lg text-xs border ${
            challenge.attempts >= challenge.max_attempts
              ? 'bg-red-50 text-red-600 border-red-200'
              : 'bg-amber-50 text-amber-700 border-amber-200'
          }`}>
            {challenge.attempts >= challenge.max_attempts
              ? `No attempts remaining (${challenge.attempts}/${challenge.max_attempts})`
              : `Limited attempts: ${challenge.attempts}/${challenge.max_attempts} used. Please verify flag before submitting.`}
          </div>
        )}
        <div className="flex gap-2">
          <input value={flagInput} onChange={e => onFlagChange(e.target.value)}
            placeholder="flag{...}"
            onKeyDown={e => e.key === 'Enter' && onSubmit()}
            className="flex-1 px-3 py-2 text-xs border border-slate-200 rounded-lg bg-white font-mono" />
          <button onClick={onSubmit} disabled={loading || !flagInput || (challenge.max_attempts > 0 && challenge.attempts >= challenge.max_attempts)}
            className={`px-4 py-2 rounded-lg text-xs font-medium transition disabled:opacity-50 ${
              challenge.max_attempts > 0
                ? 'bg-amber-500 text-white hover:bg-amber-600'
                : 'bg-emerald-600 text-white hover:bg-emerald-700'
            }`}>
            {challenge.max_attempts > 0 ? 'Submit (Limited)' : 'Submit'}
          </button>
        </div>
        {submitResult && (
          <div className={`px-3 py-2 rounded-lg text-xs ${
            submitResult.status === 'correct' ? 'bg-emerald-50 text-emerald-700 border border-emerald-200' :
            submitResult.status === 'already_solved' ? 'bg-blue-50 text-blue-700 border border-blue-200' :
            'bg-red-50 text-red-600 border border-red-200'
          }`}>
            {submitResult.message || submitResult.status}
          </div>
        )}
      </div>

      {/* Import button */}
      <button onClick={onImport} disabled={loading}
        className="w-full px-4 py-2.5 bg-indigo-600 text-white rounded-xl text-xs font-medium hover:bg-indigo-700 disabled:opacity-50 transition flex items-center justify-center gap-2">
        <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="M12 5v14m-7-7h14" /></svg>
        Import as Project
      </button>
    </div>
  )
}
