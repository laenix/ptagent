import { useState } from 'react'
import { useAppStore } from '../store'
import * as api from '../services/api'

function Modal({ show, onClose, children }: { show: boolean; onClose: () => void; children: React.ReactNode }) {
  if (!show) return null
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/20 backdrop-blur-sm modal-backdrop" onClick={onClose}>
      <div className="bg-white rounded-2xl shadow-2xl w-full max-w-lg p-6 border border-slate-200/60 modal-content" onClick={e => e.stopPropagation()}>
        {children}
      </div>
    </div>
  )
}

export function NewProjectModal() {
  const show = useAppStore(s => s.showNewProject)
  const setModal = useAppStore(s => s.setModal)
  const showToast = useAppStore(s => s.showToast)
  const loadProjects = useAppStore(s => s.loadProjects)
  const actorName = useAppStore(s => s.actorName)

  const [title, setTitle] = useState('')
  const [origin, setOrigin] = useState('')
  const [goal, setGoal] = useState('')
  const [hint, setHint] = useState('')

  const handleCreate = async () => {
    try {
      const hints = hint.trim() ? [{ content: hint.trim(), creator: actorName }] : undefined
      await api.createProject({ title, origin, goal, hints })
      setModal('showNewProject', false)
      setTitle(''); setOrigin(''); setGoal(''); setHint('')
      await loadProjects()
      showToast('Project created')
    } catch (e) { showToast((e as Error).message, 'error') }
  }

  return (
    <Modal show={show} onClose={() => setModal('showNewProject', false)}>
      <h3 className="text-base font-semibold text-slate-700 mb-4">New Project</h3>
      <div className="space-y-3">
        <input value={title} onChange={e => setTitle(e.target.value)} placeholder="Project title"
          className="w-full px-3 py-2 border border-slate-200 rounded-xl text-sm focus:outline-none focus:ring-2 focus:ring-indigo-100 focus:border-indigo-400" />
        <textarea value={origin} onChange={e => setOrigin(e.target.value)} placeholder="Origin — starting point" rows={2}
          className="w-full px-3 py-2 border border-slate-200 rounded-xl text-sm focus:outline-none focus:ring-2 focus:ring-indigo-100 focus:border-indigo-400" />
        <textarea value={goal} onChange={e => setGoal(e.target.value)} placeholder="Goal — what to achieve" rows={2}
          className="w-full px-3 py-2 border border-slate-200 rounded-xl text-sm focus:outline-none focus:ring-2 focus:ring-indigo-100 focus:border-indigo-400" />
        <input value={hint} onChange={e => setHint(e.target.value)} placeholder="Initial hint (optional)"
          className="w-full px-3 py-2 border border-slate-200 rounded-xl text-sm focus:outline-none focus:ring-2 focus:ring-indigo-100 focus:border-indigo-400" />
      </div>
      <div className="flex justify-end gap-2 mt-5">
        <button onClick={() => setModal('showNewProject', false)} className="px-4 py-2 text-sm text-slate-500 hover:bg-slate-50 rounded-xl">Cancel</button>
        <button onClick={handleCreate} disabled={!title || !origin || !goal}
          className="px-5 py-2 text-sm bg-indigo-500 text-white rounded-xl font-medium hover:bg-indigo-600 disabled:opacity-30 shadow-sm">Create</button>
      </div>
    </Modal>
  )
}

export function IntentModal() {
  const show = useAppStore(s => s.showIntentModal)
  const setModal = useAppStore(s => s.setModal)
  const showToast = useAppStore(s => s.showToast)
  const selectedProjectId = useAppStore(s => s.selectedProjectId)
  const selectedFacts = useAppStore(s => s.selectedFacts)
  const loadProject = useAppStore(s => s.loadProject)
  const actorName = useAppStore(s => s.actorName)

  const [desc, setDesc] = useState('')

  const handleCreate = async (claim: boolean) => {
    try {
      await api.createIntent(selectedProjectId, {
        from: selectedFacts,
        description: desc,
        creator: actorName,
        worker: claim ? actorName : null,
      })
      setModal('showIntentModal', false)
      setDesc('')
      await loadProject(selectedProjectId)
      showToast(claim ? 'Intent declared & claimed' : 'Intent declared')
    } catch (e) { showToast((e as Error).message, 'error') }
  }

  return (
    <Modal show={show} onClose={() => setModal('showIntentModal', false)}>
      <h3 className="text-base font-semibold text-slate-700 mb-1">New Intent</h3>
      <div className="mb-4">
        <label className="text-[11px] text-slate-400 mb-1 block font-medium">From facts</label>
        <div className="px-3 py-2 border border-slate-200 rounded-xl bg-slate-50 flex flex-wrap gap-1.5">
          {selectedFacts.map(fid => (
            <span key={fid} className="px-2 py-1 rounded-lg bg-white border border-slate-200 text-[11px] font-mono text-slate-600">{fid}</span>
          ))}
        </div>
      </div>
      <textarea value={desc} onChange={e => setDesc(e.target.value)} placeholder="What will you explore?" rows={3}
        className="w-full px-3 py-2 border border-slate-200 rounded-xl text-sm focus:outline-none focus:ring-2 focus:ring-indigo-100 focus:border-indigo-400" />
      <div className="flex justify-end gap-2 mt-5">
        <button onClick={() => setModal('showIntentModal', false)} className="px-4 py-2 text-sm text-slate-500 hover:bg-slate-50 rounded-xl">Cancel</button>
        <button onClick={() => handleCreate(false)} disabled={!desc}
          className="px-5 py-2 text-sm border border-slate-200 text-slate-600 rounded-xl font-medium hover:bg-slate-50 disabled:opacity-30">Declare</button>
        <button onClick={() => handleCreate(true)} disabled={!desc}
          className="px-5 py-2 text-sm bg-indigo-500 text-white rounded-xl font-medium hover:bg-indigo-600 disabled:opacity-30 shadow-sm">Declare & Claim</button>
      </div>
    </Modal>
  )
}

export function ConcludeModal() {
  const show = useAppStore(s => s.showConcludeModal)
  const setModal = useAppStore(s => s.setModal)
  const showToast = useAppStore(s => s.showToast)
  const selectedProjectId = useAppStore(s => s.selectedProjectId)
  const selectedNode = useAppStore(s => s.selectedNode)
  const loadProject = useAppStore(s => s.loadProject)
  const actorName = useAppStore(s => s.actorName)

  const [desc, setDesc] = useState('')

  const handleConclude = async () => {
    if (!selectedNode || selectedNode.type !== 'intent') return
    try {
      await api.concludeIntent(selectedProjectId, selectedNode.id, { worker: actorName, description: desc })
      setModal('showConcludeModal', false)
      setDesc('')
      await loadProject(selectedProjectId)
      showToast('Intent concluded')
    } catch (e) { showToast((e as Error).message, 'error') }
  }

  return (
    <Modal show={show} onClose={() => setModal('showConcludeModal', false)}>
      <h3 className="text-base font-semibold text-slate-700 mb-1">Conclude Intent</h3>
      <p className="text-xs text-slate-400 mb-4 font-mono">{selectedNode?.id}</p>
      <textarea value={desc} onChange={e => setDesc(e.target.value)} placeholder="What did you find? (new fact)" rows={3}
        className="w-full px-3 py-2 border border-slate-200 rounded-xl text-sm focus:outline-none focus:ring-2 focus:ring-indigo-100 focus:border-indigo-400" />
      <div className="flex justify-end gap-2 mt-5">
        <button onClick={() => setModal('showConcludeModal', false)} className="px-4 py-2 text-sm text-slate-500 hover:bg-slate-50 rounded-xl">Cancel</button>
        <button onClick={handleConclude} disabled={!desc}
          className="px-5 py-2 text-sm bg-teal-500 text-white rounded-xl font-medium hover:bg-teal-600 disabled:opacity-30 shadow-sm">Conclude</button>
      </div>
    </Modal>
  )
}

export function CompleteModal() {
  const show = useAppStore(s => s.showCompleteModal)
  const setModal = useAppStore(s => s.setModal)
  const showToast = useAppStore(s => s.showToast)
  const selectedProjectId = useAppStore(s => s.selectedProjectId)
  const selectedFacts = useAppStore(s => s.selectedFacts)
  const loadProject = useAppStore(s => s.loadProject)
  const loadProjects = useAppStore(s => s.loadProjects)
  const actorName = useAppStore(s => s.actorName)

  const [desc, setDesc] = useState('')

  const handleComplete = async () => {
    try {
      await api.completeProject(selectedProjectId, { from: selectedFacts, description: desc, worker: actorName })
      setModal('showCompleteModal', false)
      setDesc('')
      await loadProjects()
      await loadProject(selectedProjectId)
      showToast('Project completed')
    } catch (e) { showToast((e as Error).message, 'error') }
  }

  return (
    <Modal show={show} onClose={() => setModal('showCompleteModal', false)}>
      <h3 className="text-base font-semibold text-slate-700 mb-1">Complete Project</h3>
      <p className="text-xs text-slate-400 mb-4">Mark as completed with selected facts</p>
      <div className="mb-3 px-3 py-2 border border-slate-200 rounded-xl bg-slate-50 flex flex-wrap gap-1.5">
        {selectedFacts.map(fid => (
          <span key={fid} className="px-2 py-1 rounded-lg bg-white border border-slate-200 text-[11px] font-mono text-slate-600">{fid}</span>
        ))}
      </div>
      <textarea value={desc} onChange={e => setDesc(e.target.value)} placeholder="Why is the goal met?" rows={2}
        className="w-full px-3 py-2 border border-slate-200 rounded-xl text-sm focus:outline-none focus:ring-2 focus:ring-indigo-100 focus:border-indigo-400" />
      <div className="flex justify-end gap-2 mt-5">
        <button onClick={() => setModal('showCompleteModal', false)} className="px-4 py-2 text-sm text-slate-500 hover:bg-slate-50 rounded-xl">Cancel</button>
        <button onClick={handleComplete} disabled={!desc || selectedFacts.length === 0}
          className="px-5 py-2 text-sm bg-teal-500 text-white rounded-xl font-medium hover:bg-teal-600 disabled:opacity-30 shadow-sm">Complete</button>
      </div>
    </Modal>
  )
}

export function HintModal() {
  const show = useAppStore(s => s.showHintModal)
  const setModal = useAppStore(s => s.setModal)
  const showToast = useAppStore(s => s.showToast)
  const selectedProjectId = useAppStore(s => s.selectedProjectId)
  const loadProject = useAppStore(s => s.loadProject)
  const actorName = useAppStore(s => s.actorName)

  const [content, setContent] = useState('')

  const handleAdd = async () => {
    try {
      await api.createHint(selectedProjectId, content, actorName)
      setModal('showHintModal', false)
      setContent('')
      await loadProject(selectedProjectId)
      showToast('Hint added')
    } catch (e) { showToast((e as Error).message, 'error') }
  }

  return (
    <Modal show={show} onClose={() => setModal('showHintModal', false)}>
      <h3 className="text-base font-semibold text-slate-700 mb-4">Add Hint</h3>
      <textarea value={content} onChange={e => setContent(e.target.value)} placeholder="Strategy advice or note..." rows={3}
        className="w-full px-3 py-2 border border-slate-200 rounded-xl text-sm focus:outline-none focus:ring-2 focus:ring-indigo-100 focus:border-indigo-400" />
      <div className="flex justify-end gap-2 mt-5">
        <button onClick={() => setModal('showHintModal', false)} className="px-4 py-2 text-sm text-slate-500 hover:bg-slate-50 rounded-xl">Cancel</button>
        <button onClick={handleAdd} disabled={!content}
          className="px-5 py-2 text-sm bg-amber-500 text-white rounded-xl font-medium hover:bg-amber-600 disabled:opacity-30 shadow-sm">Add</button>
      </div>
    </Modal>
  )
}

export function DeleteModal() {
  const show = useAppStore(s => s.showDeleteModal)
  const setModal = useAppStore(s => s.setModal)
  const showToast = useAppStore(s => s.showToast)
  const selectedProjectId = useAppStore(s => s.selectedProjectId)
  const loadProjects = useAppStore(s => s.loadProjects)

  const handleDelete = async () => {
    try {
      await api.deleteProject(selectedProjectId)
      setModal('showDeleteModal', false)
      await loadProjects()
      showToast('Project deleted')
      window.location.hash = '/'
    } catch (e) { showToast((e as Error).message, 'error') }
  }

  return (
    <Modal show={show} onClose={() => setModal('showDeleteModal', false)}>
      <h3 className="text-base font-semibold text-slate-700 mb-1">Delete Project</h3>
      <p className="text-sm text-slate-500 mt-1">This will permanently remove <span className="font-medium text-slate-700">{selectedProjectId}</span>.</p>
      <p className="text-xs text-slate-400 mt-3">This action cannot be undone.</p>
      <div className="flex justify-end gap-2 mt-6">
        <button onClick={() => setModal('showDeleteModal', false)} className="px-4 py-2 text-sm text-slate-500 hover:bg-slate-50 rounded-xl">Cancel</button>
        <button onClick={handleDelete}
          className="px-4 py-2 text-sm bg-rose-500 text-white rounded-xl font-medium hover:bg-rose-600 shadow-sm">Delete</button>
      </div>
    </Modal>
  )
}

export function ReopenModal() {
  const show = useAppStore(s => s.showReopenModal)
  const setModal = useAppStore(s => s.setModal)
  const showToast = useAppStore(s => s.showToast)
  const selectedProjectId = useAppStore(s => s.selectedProjectId)
  const loadProject = useAppStore(s => s.loadProject)
  const loadProjects = useAppStore(s => s.loadProjects)
  const actorName = useAppStore(s => s.actorName)

  const [desc, setDesc] = useState('')

  const handleReopen = async () => {
    try {
      await api.reopenProject(selectedProjectId, { description: desc, creator: actorName })
      setModal('showReopenModal', false)
      setDesc('')
      await loadProjects()
      await loadProject(selectedProjectId)
      showToast('Project reopened')
    } catch (e) { showToast((e as Error).message, 'error') }
  }

  return (
    <Modal show={show} onClose={() => setModal('showReopenModal', false)}>
      <h3 className="text-base font-semibold text-slate-700 mb-1">Reopen Project</h3>
      <p className="text-xs text-slate-400 mb-4">Reactivate this completed project with a new direction</p>
      <textarea value={desc} onChange={e => setDesc(e.target.value)} placeholder="Why reopen? What's the new direction?" rows={3}
        className="w-full px-3 py-2 border border-slate-200 rounded-xl text-sm focus:outline-none focus:ring-2 focus:ring-indigo-100 focus:border-indigo-400" />
      <div className="flex justify-end gap-2 mt-5">
        <button onClick={() => setModal('showReopenModal', false)} className="px-4 py-2 text-sm text-slate-500 hover:bg-slate-50 rounded-xl">Cancel</button>
        <button onClick={handleReopen} disabled={!desc}
          className="px-5 py-2 text-sm bg-indigo-500 text-white rounded-xl font-medium hover:bg-indigo-600 disabled:opacity-30 shadow-sm">Reopen</button>
      </div>
    </Modal>
  )
}

export function SettingsModal() {
  const show = useAppStore(s => s.showSettingsModal)
  const setModal = useAppStore(s => s.setModal)
  const showToast = useAppStore(s => s.showToast)
  const settings = useAppStore(s => s.settings)
  const loadSettings = useAppStore(s => s.loadSettings)

  const [intentTimeout, setIntentTimeout] = useState(settings.intent_timeout)
  const [reasonTimeout, setReasonTimeout] = useState(settings.reason_timeout)

  const handleSave = async () => {
    try {
      await api.updateSettings({ intent_timeout: intentTimeout, reason_timeout: reasonTimeout })
      setModal('showSettingsModal', false)
      await loadSettings()
      showToast('Settings saved')
    } catch (e) { showToast((e as Error).message, 'error') }
  }

  return (
    <Modal show={show} onClose={() => setModal('showSettingsModal', false)}>
      <h3 className="text-base font-semibold text-slate-700 mb-4">Server Settings</h3>
      <div className="space-y-4">
        <div>
          <label className="text-[11px] text-slate-400 mb-1 block font-medium">Intent timeout (seconds)</label>
          <input type="number" value={intentTimeout} onChange={e => setIntentTimeout(Number(e.target.value))}
            className="w-full px-3 py-2 border border-slate-200 rounded-xl text-sm focus:outline-none focus:ring-2 focus:ring-indigo-100" />
        </div>
        <div>
          <label className="text-[11px] text-slate-400 mb-1 block font-medium">Reason timeout (seconds)</label>
          <input type="number" value={reasonTimeout} onChange={e => setReasonTimeout(Number(e.target.value))}
            className="w-full px-3 py-2 border border-slate-200 rounded-xl text-sm focus:outline-none focus:ring-2 focus:ring-indigo-100" />
        </div>
      </div>
      <div className="flex justify-end gap-2 mt-5">
        <button onClick={() => setModal('showSettingsModal', false)} className="px-4 py-2 text-sm text-slate-500 hover:bg-slate-50 rounded-xl">Close</button>
        <button onClick={handleSave} className="px-5 py-2 text-sm bg-indigo-500 text-white rounded-xl font-medium hover:bg-indigo-600 shadow-sm">Save</button>
      </div>
    </Modal>
  )
}

export function ExportModal() {
  const show = useAppStore(s => s.showExportModal)
  const setModal = useAppStore(s => s.setModal)
  const showToast = useAppStore(s => s.showToast)
  const selectedProjectId = useAppStore(s => s.selectedProjectId)

  const [tab, setTab] = useState<'yaml' | 'timeline'>('yaml')
  const [content, setContent] = useState('')

  const loadExport = async (format: 'yaml' | 'timeline') => {
    try {
      const text = await api.exportProject(selectedProjectId, format)
      setContent(text)
    } catch (e) { showToast((e as Error).message, 'error') }
  }

  const handleTabChange = (t: 'yaml' | 'timeline') => {
    setTab(t)
    loadExport(t)
  }

  // Load on first show
  if (show && !content) loadExport(tab)

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(content)
      showToast('Copied')
    } catch { showToast('Copy failed', 'error') }
  }

  if (!show) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/20 backdrop-blur-sm modal-backdrop" onClick={() => { setModal('showExportModal', false); setContent('') }}>
      <div className="bg-white rounded-2xl shadow-2xl w-full max-w-4xl mx-4 border border-slate-200/60 overflow-hidden modal-content" onClick={e => e.stopPropagation()}>
        <div className="px-5 py-4 border-b border-slate-200/80 flex items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <div className="flex rounded-lg border border-slate-200 overflow-hidden">
              <button onClick={() => handleTabChange('yaml')} className={`px-2.5 py-1 text-xs font-medium ${tab === 'yaml' ? 'bg-slate-100 text-slate-700' : 'text-slate-400'}`}>YAML</button>
              <button onClick={() => handleTabChange('timeline')} className={`px-2.5 py-1 text-xs font-medium ${tab === 'timeline' ? 'bg-slate-100 text-slate-700' : 'text-slate-400'}`}>Timeline</button>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <button onClick={handleCopy} className="px-3 py-1.5 text-sm text-slate-500 hover:bg-slate-50 rounded-xl">Copy</button>
            <button onClick={() => { setModal('showExportModal', false); setContent('') }} className="px-3 py-1.5 text-sm text-slate-500 hover:bg-slate-50 rounded-xl">Close</button>
          </div>
        </div>
        <div className="max-h-[75vh] overflow-auto bg-slate-50 p-4">
          <pre className="rounded-xl border border-slate-200 bg-white p-4 text-xs font-mono whitespace-pre-wrap overflow-auto">{content}</pre>
        </div>
      </div>
    </div>
  )
}

export default function Modals() {
  return (
    <>
      <NewProjectModal />
      <IntentModal />
      <ConcludeModal />
      <CompleteModal />
      <HintModal />
      <DeleteModal />
      <ReopenModal />
      <SettingsModal />
      <ExportModal />
    </>
  )
}
