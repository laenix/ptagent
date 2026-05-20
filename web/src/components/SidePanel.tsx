import { useAppStore } from '../store'
import { useState, useEffect } from 'react'
import type { Intent, Fact } from '../services/api'

function formatTime(ts: string | null) {
  if (!ts) return '—'
  return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function DetailTab() {
  const project = useAppStore(s => s.project)
  const selectedNode = useAppStore(s => s.selectedNode)
  const selectedFacts = useAppStore(s => s.selectedFacts)

  if (!project || !selectedNode) {
    return (
      <div className="rounded-2xl border border-dashed border-slate-200 bg-white/80 px-5 py-10 text-center shadow-sm">
        <p className="text-sm text-slate-400">Click a node or edge</p>
        <p className="text-xs text-slate-300 mt-1">Shift+click for multi-select</p>
      </div>
    )
  }

  if (selectedNode.type === 'fact') {
    const facts = selectedFacts
      .map(fid => project.facts.find(f => f.id === fid))
      .filter(Boolean) as Fact[]

    return (
      <div className="space-y-3">
        {facts.map(fact => {
          const producing = project.intents.find(i => i.to === fact.id)
          return (
            <article key={fact.id} className="rounded-2xl border border-slate-200 bg-white shadow-sm overflow-hidden">
              <div className="px-4 py-3 border-b border-slate-100 bg-slate-50/80">
                <div className="flex items-center gap-2">
                  <span className={`px-2 py-0.5 rounded-md text-[10px] font-mono font-bold ${
                    fact.id === 'origin' ? 'bg-teal-50 text-teal-700' :
                    fact.id === 'goal' ? 'bg-rose-50 text-rose-600' :
                    'bg-brand-50 text-brand-700'
                  }`}>{fact.id}</span>
                  <span className="text-[11px] text-slate-400 uppercase tracking-wider">Fact</span>
                </div>
              </div>
              <div className="p-4 space-y-3">
                <p className="text-sm text-slate-700 leading-relaxed whitespace-pre-wrap break-words">{fact.description}</p>
                {producing && (
                  <div className="pt-3 border-t border-slate-100 space-y-2 text-xs text-slate-400">
                    <div className="flex justify-between gap-3">
                      <span>Produced by</span>
                      <span className="font-mono text-slate-600">{producing.id}</span>
                    </div>
                    <div className="flex justify-between gap-3">
                      <span>From</span>
                      <span className="font-mono text-slate-600">{producing.from.join(', ')}</span>
                    </div>
                    <div className="flex justify-between gap-3">
                      <span>Worker</span>
                      <span className="text-slate-600">{producing.worker || '—'}</span>
                    </div>
                    <div className="flex justify-between gap-3">
                      <span>Concluded</span>
                      <span className="text-slate-600">{formatTime(producing.concluded_at)}</span>
                    </div>
                  </div>
                )}
              </div>
            </article>
          )
        })}
      </div>
    )
  }

  if (selectedNode.type === 'intent') {
    const intent = project.intents.find(i => i.id === selectedNode.id)
    if (!intent) return null
    return <IntentDetail intent={intent} />
  }

  return null
}

function IntentDetail({ intent }: { intent: Intent }) {
  const statusLabel = intent.to ? 'Concluded' : intent.worker ? 'In Progress' : 'Unclaimed'
  const statusClass = intent.to ? 'text-teal-600' : intent.worker ? 'text-amber-600' : 'text-slate-400'

  return (
    <section className="rounded-2xl border border-violet-200/80 bg-white shadow-sm overflow-hidden">
      <div className="px-4 py-3 border-b border-violet-100 bg-violet-50/70">
        <div className="flex items-center gap-2">
          <span className="px-2 py-0.5 rounded-md bg-violet-100 text-violet-700 text-[10px] font-mono font-bold">
            {intent.id}
          </span>
          <span className={`text-[11px] font-medium ${statusClass}`}>{statusLabel}</span>
        </div>
      </div>
      <div className="p-4 space-y-4">
        <p className="text-sm text-slate-700 leading-relaxed whitespace-pre-wrap break-words">{intent.description}</p>
        <div className="pt-4 border-t border-slate-100 space-y-3 text-xs">
          <Row label="From" value={intent.from.join(', ')} mono />
          <Row label="To" value={intent.to || '—'} mono />
          <Row label="Creator" value={intent.creator} />
          <Row label="Worker" value={intent.worker || '—'} />
          <Row label="Heartbeat" value={formatTime(intent.last_heartbeat_at)} />
          <Row label="Created" value={formatTime(intent.created_at)} />
          {intent.concluded_at && <Row label="Concluded" value={formatTime(intent.concluded_at)} />}
        </div>
      </div>
    </section>
  )
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-3">
      <span className="text-slate-400 shrink-0">{label}</span>
      <span className={`text-slate-600 text-right break-all ${mono ? 'font-mono' : ''}`}>{value}</span>
    </div>
  )
}

function HintsTab() {
  const project = useAppStore(s => s.project)
  if (!project || project.hints.length === 0) {
    return (
      <div className="rounded-2xl border border-dashed border-amber-200 bg-white/80 px-5 py-10 text-center shadow-sm">
        <p className="text-sm text-slate-300">No hints yet</p>
      </div>
    )
  }

  return (
    <div className="space-y-3">
      {project.hints.map(h => (
        <div key={h.id} className="rounded-2xl border border-amber-200/80 bg-white shadow-sm overflow-hidden">
          <div className="px-4 py-3 border-b border-amber-100 bg-amber-50/80">
            <div className="flex items-center justify-between text-[11px]">
              <div className="flex items-center gap-2">
                <span className="w-2 h-2 rounded-full bg-amber-400" />
                <span className="font-medium text-amber-800">{h.creator}</span>
              </div>
              <span className="text-amber-600/80 tabular-nums">{formatTime(h.created_at)}</span>
            </div>
          </div>
          <div className="p-4">
            <p className="text-sm text-slate-700 leading-relaxed whitespace-pre-wrap break-words">{h.content}</p>
          </div>
        </div>
      ))}
    </div>
  )
}

interface TimelineEvent {
  id: string
  type: string
  timestamp: string
  actor: string
  title: string
  badge: string
  badgeClass: string
  dotClass: string
}

function buildTimeline(project: import('../services/api').ProjectDetail): TimelineEvent[] {
  const events: TimelineEvent[] = []

  events.push({
    id: `created-${project.project.id}`,
    type: 'project_created',
    timestamp: project.project.created_at,
    actor: 'system',
    title: project.project.title,
    badge: 'Project',
    badgeClass: 'bg-slate-100 text-slate-600',
    dotClass: 'bg-slate-400',
  })

  for (const hint of project.hints) {
    events.push({
      id: `hint-${hint.id}`,
      type: 'hint_added',
      timestamp: hint.created_at,
      actor: hint.creator,
      title: hint.content,
      badge: 'Hint',
      badgeClass: 'bg-amber-50 text-amber-700',
      dotClass: 'bg-amber-400',
    })
  }

  for (const intent of project.intents) {
    events.push({
      id: `declared-${intent.id}`,
      type: 'intent_declared',
      timestamp: intent.created_at,
      actor: intent.creator,
      title: intent.description,
      badge: 'Intent',
      badgeClass: 'bg-violet-50 text-violet-700',
      dotClass: 'bg-violet-400',
    })

    if (intent.concluded_at && intent.to) {
      const isComplete = intent.to === 'goal'
      events.push({
        id: `concluded-${intent.id}`,
        type: isComplete ? 'project_completed' : 'intent_concluded',
        timestamp: intent.concluded_at,
        actor: intent.worker || intent.creator,
        title: isComplete ? 'Goal reached' : `Produced ${intent.to}`,
        badge: isComplete ? 'Complete' : 'Conclude',
        badgeClass: isComplete ? 'bg-rose-50 text-rose-700' : 'bg-teal-50 text-teal-700',
        dotClass: isComplete ? 'bg-rose-400' : 'bg-teal-400',
      })
    }
  }

  events.sort((a, b) => a.timestamp.localeCompare(b.timestamp))
  return events
}

function LogTab() {
  const project = useAppStore(s => s.project)
  if (!project) return <p className="text-sm text-slate-300 text-center mt-12">No activity yet</p>

  const events = buildTimeline(project)
  if (events.length === 0) return <p className="text-sm text-slate-300 text-center mt-12">No activity yet</p>

  return (
    <div className="space-y-3">
      {events.map((entry, idx) => (
        <div key={entry.id} className="flex items-stretch gap-3">
          <div className="flex flex-col items-center shrink-0">
            <div className={`w-2.5 h-2.5 rounded-full mt-1.5 ${entry.dotClass}`} />
            {idx < events.length - 1 && <div className="w-px flex-1 bg-slate-100 mt-2" />}
          </div>
          <div className="min-w-0 flex-1 text-left rounded-xl px-0.5 py-0.5">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-[11px] font-medium text-slate-400 tabular-nums">{formatTime(entry.timestamp)}</span>
              <span className={`px-2 py-0.5 rounded-md text-[10px] font-semibold uppercase tracking-wide ${entry.badgeClass}`}>
                {entry.badge}
              </span>
            </div>
            <p className="text-sm text-slate-700 leading-relaxed break-words mt-1">{entry.title}</p>
            {entry.actor && <p className="text-[11px] text-slate-400 mt-1">{entry.actor}</p>}
          </div>
        </div>
      ))}
    </div>
  )
}

function phaseColor(phase: string): { dot: string; badge: string } {
  switch (phase) {
    case 'dispatched': return { dot: 'bg-blue-400', badge: 'bg-blue-50 text-blue-700' }
    case 'succeed': return { dot: 'bg-emerald-400', badge: 'bg-emerald-50 text-emerald-700' }
    case 'failed': return { dot: 'bg-red-400', badge: 'bg-red-50 text-red-700' }
    case 'rejected': return { dot: 'bg-amber-400', badge: 'bg-amber-50 text-amber-700' }
    case 'cancelled': return { dot: 'bg-slate-400', badge: 'bg-slate-100 text-slate-600' }
    case 'healthcheck_failed': return { dot: 'bg-orange-400', badge: 'bg-orange-50 text-orange-700' }
    case 'concluded': return { dot: 'bg-indigo-400', badge: 'bg-indigo-50 text-indigo-700' }
    default: return { dot: 'bg-slate-300', badge: 'bg-slate-100 text-slate-500' }
  }
}

function ExpandableText({ label, text }: { label: string; text: string }) {
  const [open, setOpen] = useState(false)
  if (!text) return null
  return (
    <div className="mt-2">
      <button onClick={() => setOpen(!open)} className="text-[11px] text-indigo-500 hover:text-indigo-700 font-medium flex items-center gap-1">
        <svg className={`w-3 h-3 transition-transform ${open ? 'rotate-90' : ''}`} fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24"><path d="m9 5 7 7-7 7" /></svg>
        {label}
      </button>
      {open && (
        <pre className="mt-1 p-2 bg-slate-50 rounded-lg text-[11px] text-slate-600 font-mono overflow-x-auto max-h-64 overflow-y-auto whitespace-pre-wrap break-all border border-slate-100">
          {text}
        </pre>
      )}
    </div>
  )
}

function ReplayTab() {
  const project = useAppStore(s => s.project)
  const taskEvents = useAppStore(s => s.taskEvents)
  const loadTaskEvents = useAppStore(s => s.loadTaskEvents)
  const [filter, setFilter] = useState<{ task_type: string; phase: string }>({ task_type: '', phase: '' })

  useEffect(() => {
    if (project?.project.id) loadTaskEvents(project.project.id)
    const interval = setInterval(() => {
      if (project?.project.id) loadTaskEvents(project.project.id)
    }, 5000)
    return () => clearInterval(interval)
  }, [project?.project.id, loadTaskEvents])

  const filtered = taskEvents.filter(e => {
    if (filter.task_type && e.task_type !== filter.task_type) return false
    if (filter.phase && e.phase !== filter.phase) return false
    return true
  })

  return (
    <div className="space-y-3">
      {/* Filters */}
      <div className="flex gap-2">
        <select value={filter.task_type} onChange={e => setFilter(f => ({ ...f, task_type: e.target.value }))}
          className="flex-1 px-2 py-1 text-[11px] border border-slate-200 rounded-lg bg-white text-slate-600">
          <option value="">All types</option>
          <option value="bootstrap">Bootstrap</option>
          <option value="reason">Reason</option>
          <option value="explore">Explore</option>
        </select>
        <select value={filter.phase} onChange={e => setFilter(f => ({ ...f, phase: e.target.value }))}
          className="flex-1 px-2 py-1 text-[11px] border border-slate-200 rounded-lg bg-white text-slate-600">
          <option value="">All phases</option>
          <option value="dispatched">Dispatched</option>
          <option value="succeed">Succeed</option>
          <option value="failed">Failed</option>
          <option value="rejected">Rejected</option>
          <option value="concluded">Concluded</option>
          <option value="healthcheck_failed">HC Failed</option>
        </select>
      </div>

      {filtered.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-slate-200 bg-white/80 px-5 py-10 text-center shadow-sm">
          <p className="text-sm text-slate-300">No task events yet</p>
        </div>
      ) : (
        filtered.map((ev, idx) => {
          const colors = phaseColor(ev.phase)
          return (
            <div key={ev.id} className="flex items-stretch gap-3">
              <div className="flex flex-col items-center shrink-0">
                <div className={`w-2.5 h-2.5 rounded-full mt-1.5 ${colors.dot}`} />
                {idx < filtered.length - 1 && <div className="w-px flex-1 bg-slate-100 mt-2" />}
              </div>
              <div className="min-w-0 flex-1 rounded-xl px-0.5 py-0.5 mb-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-[11px] font-medium text-slate-400 tabular-nums">{formatTime(ev.created_at)}</span>
                  <span className={`px-2 py-0.5 rounded-md text-[10px] font-semibold uppercase tracking-wide ${colors.badge}`}>
                    {ev.phase}
                  </span>
                  <span className="px-1.5 py-0.5 rounded text-[10px] font-mono bg-slate-50 text-slate-500">{ev.task_type}</span>
                </div>
                <div className="mt-1 flex flex-wrap gap-x-4 gap-y-0.5 text-[11px] text-slate-500">
                  <span>worker: <span className="text-slate-700">{ev.worker}</span></span>
                  {ev.intent_id && <span>intent: <span className="font-mono text-slate-700">{ev.intent_id}</span></span>}
                  {ev.duration_ms > 0 && <span>{(ev.duration_ms / 1000).toFixed(1)}s</span>}
                </div>
                {ev.error && <p className="mt-1 text-[11px] text-red-500 break-words">{ev.error}</p>}
                <ExpandableText label="Prompt" text={ev.prompt} />
                <ExpandableText label="Output" text={ev.output} />
              </div>
            </div>
          )
        })
      )}
    </div>
  )
}

function LiveTab() {
  const liveEvents = useAppStore(s => s.liveEvents)
  const sseConnected = useAppStore(s => s.sseConnected)
  const project = useAppStore(s => s.project)

  const projectEvents = project
    ? liveEvents.filter(e => e.project_id === project.project.id || !e.project_id)
    : liveEvents

  const eventColor = (type: string) => {
    if (type.includes('completed') || type.includes('concluded')) return { dot: 'bg-emerald-400', text: 'text-emerald-700', bg: 'bg-emerald-50' }
    if (type.includes('failed')) return { dot: 'bg-red-400', text: 'text-red-700', bg: 'bg-red-50' }
    if (type.includes('dispatched')) return { dot: 'bg-blue-400', text: 'text-blue-700', bg: 'bg-blue-50' }
    if (type.includes('created')) return { dot: 'bg-violet-400', text: 'text-violet-700', bg: 'bg-violet-50' }
    return { dot: 'bg-slate-300', text: 'text-slate-600', bg: 'bg-slate-50' }
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 text-[11px] text-slate-400 mb-3">
        <span className={`inline-block w-2 h-2 rounded-full ${sseConnected ? 'bg-emerald-400 animate-pulse' : 'bg-red-400'}`} />
        <span>{sseConnected ? 'Connected' : 'Disconnected'}</span>
        <span className="ml-auto">{projectEvents.length} events</span>
      </div>

      {projectEvents.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-slate-200 bg-white/80 px-5 py-10 text-center shadow-sm">
          <p className="text-sm text-slate-300">Waiting for events...</p>
          <p className="text-xs text-slate-300 mt-1">Events will appear here in real-time</p>
        </div>
      ) : (
        [...projectEvents].reverse().map((ev, idx) => {
          const colors = eventColor(ev.type)
          return (
            <div key={idx} className="flex items-start gap-2">
              <div className={`w-2 h-2 rounded-full mt-1.5 shrink-0 ${colors.dot}`} />
              <div className="min-w-0 flex-1">
                <span className={`inline-block px-1.5 py-0.5 rounded text-[10px] font-semibold ${colors.bg} ${colors.text}`}>
                  {ev.type.replace(/_/g, ' ')}
                </span>
                {ev.data && typeof ev.data === 'object' ? (
                  <p className="text-[11px] text-slate-500 mt-0.5 break-words truncate">
                    {JSON.stringify(ev.data).slice(0, 120)}
                  </p>
                ) : null}
              </div>
            </div>
          )
        })
      )}
    </div>
  )
}

export default function SidePanel() {
  const sideTab = useAppStore(s => s.sideTab)
  const setSideTab = useAppStore(s => s.setSideTab)
  const sidePanelWidth = useAppStore(s => s.sidePanelWidth)

  return (
    <div
      className="border-l border-slate-200/60 bg-white flex flex-col overflow-hidden shrink-0"
      style={{ width: sidePanelWidth, minWidth: sidePanelWidth }}
    >
      {/* Tabs */}
      <div className="flex border-b border-slate-100 shrink-0">
        {(['detail', 'hints', 'log', 'replay', 'live'] as const).map(tab => (
          <button
            key={tab}
            onClick={() => setSideTab(tab)}
            className={`flex-1 px-3 py-2.5 text-xs font-medium transition capitalize ${
              sideTab === tab
                ? 'text-indigo-600 border-b-2 border-indigo-500'
                : 'text-slate-400 hover:text-slate-600'
            }`}
          >
            {tab === 'live' ? <span className="flex items-center gap-1">
              <span className={`inline-block w-1.5 h-1.5 rounded-full ${useAppStore.getState().sseConnected ? 'bg-emerald-400 animate-pulse' : 'bg-slate-300'}`} />
              Live
            </span> : tab}
          </button>
        ))}
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto p-4">
        {sideTab === 'detail' && <DetailTab />}
        {sideTab === 'hints' && <HintsTab />}
        {sideTab === 'log' && <LogTab />}
        {sideTab === 'replay' && <ReplayTab />}
        {sideTab === 'live' && <LiveTab />}
      </div>
    </div>
  )
}
