import { useEffect, useRef, useCallback } from 'react'
import cytoscape, { Core, EventObject } from 'cytoscape'
import { useAppStore, LayoutMode } from '../store'
import type { ProjectDetail, Intent } from '../services/api'

function isBootstrapIntent(intent: Intent): boolean {
  return (
    intent.description === 'bootstrap' &&
    intent.creator === 'dispatcher.bootstrap' &&
    Array.isArray(intent.from) &&
    intent.from.length === 1 &&
    intent.from[0] === 'origin' &&
    intent.to === null
  )
}

function openIntentNodeType(intent: Intent): string {
  if (isBootstrapIntent(intent)) return intent.worker ? 'bootstrap_running' : 'bootstrap_pending'
  return intent.worker ? 'in_progress' : 'unclaimed'
}

function summarizeLabel(description: string, maxLen = 24): string {
  const normalized = description.replace(/\s+/g, ' ').trim()
  const chars = Array.from(normalized)
  if (chars.length <= maxLen) return normalized
  return `${chars.slice(0, maxLen).join('')}…`
}

function buildElements(project: ProjectDetail) {
  const nodes: cytoscape.ElementDefinition[] = []
  const edges: cytoscape.ElementDefinition[] = []

  for (const f of project.facts) {
    const nodeType = f.id === 'origin' ? 'origin' : f.id === 'goal' ? 'goal' : 'fact'
    nodes.push({
      data: {
        id: f.id,
        label: f.id === 'origin' ? 'Origin' : f.id === 'goal' ? 'Goal' : summarizeLabel(f.description),
        description: f.description,
        nodeType,
      },
    })
  }

  for (const intent of project.intents) {
    if (intent.to) {
      // Concluded intent -> edges from sources to produced fact
      for (const src of intent.from) {
        edges.push({
          data: {
            id: `${intent.id}_${src}`,
            source: src,
            target: intent.to,
            intentId: intent.id,
            label: summarizeLabel(intent.description, 16),
            status: 'concluded',
          },
        })
      }
    } else {
      // Open intent -> placeholder node + edges
      const phId = `_ph_${intent.id}`
      const nodeType = openIntentNodeType(intent)
      nodes.push({
        data: {
          id: phId,
          label: isBootstrapIntent(intent) ? 'Bootstrap' : '?',
          description: intent.description,
          nodeType,
          intentId: intent.id,
        },
      })
      for (const src of intent.from) {
        edges.push({
          data: {
            id: `${intent.id}_${src}`,
            source: src,
            target: phId,
            intentId: intent.id,
            label: summarizeLabel(intent.description, 16),
            status: nodeType,
          },
        })
      }
      if (isBootstrapIntent(intent)) {
        edges.push({
          data: {
            id: `${intent.id}_goal`,
            source: phId,
            target: 'goal',
            intentId: intent.id,
            label: '',
            status: nodeType,
            edgeType: 'bootstrap_scope',
          },
        })
      }
    }
  }

  return { nodes, edges }
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function graphStyles(): any[] {
  return [
    {
      selector: 'node[nodeType="origin"]',
      style: {
        shape: 'round-rectangle',
        'background-color': '#14b8a6',
        label: 'data(label)',
        color: '#fff',
        'font-size': '11px',
        'font-weight': 'bold',
        'text-valign': 'center',
        'text-halign': 'center',
        'text-wrap': 'wrap',
        'text-max-width': '92px',
        width: 100,
        height: 40,
      },
    },
    {
      selector: 'node[nodeType="goal"]',
      style: {
        shape: 'round-rectangle',
        'background-color': '#f43f5e',
        label: 'data(label)',
        color: '#fff',
        'font-size': '11px',
        'font-weight': 'bold',
        'text-valign': 'center',
        'text-halign': 'center',
        'text-wrap': 'wrap',
        'text-max-width': '92px',
        width: 100,
        height: 40,
      },
    },
    {
      selector: 'node[nodeType="fact"]',
      style: {
        shape: 'round-rectangle',
        'background-color': '#6366f1',
        label: 'data(label)',
        color: '#fff',
        'font-size': '10px',
        'font-weight': 'bold',
        'text-valign': 'center',
        'text-halign': 'center',
        'text-wrap': 'wrap',
        'text-max-width': '116px',
        width: 120,
        height: 36,
      },
    },
    {
      selector: 'node[nodeType="in_progress"]',
      style: {
        shape: 'ellipse',
        'background-color': '#f59e0b',
        'background-opacity': 0.7,
        label: '?',
        color: '#fff',
        'font-size': '11px',
        'font-weight': 'bold',
        'text-valign': 'center',
        'text-halign': 'center',
        width: 22,
        height: 22,
        'border-width': 2,
        'border-color': '#d97706',
      },
    },
    {
      selector: 'node[nodeType="unclaimed"]',
      style: {
        shape: 'ellipse',
        'background-color': '#cbd5e1',
        'background-opacity': 0.5,
        label: '?',
        color: '#94a3b8',
        'font-size': '11px',
        'font-weight': 'bold',
        'text-valign': 'center',
        'text-halign': 'center',
        width: 20,
        height: 20,
        'border-width': 1.5,
        'border-color': '#94a3b8',
        'border-style': 'dashed',
      },
    },
    {
      selector: 'node[nodeType="bootstrap_pending"]',
      style: {
        shape: 'round-rectangle',
        'background-color': '#fff7ed',
        label: 'data(label)',
        color: '#c2410c',
        'font-size': '10px',
        'font-weight': 'bold',
        'text-valign': 'center',
        'text-halign': 'center',
        width: 82,
        height: 30,
        'border-width': 1.5,
        'border-color': '#fdba74',
        'border-style': 'dashed',
      },
    },
    {
      selector: 'node[nodeType="bootstrap_running"]',
      style: {
        shape: 'round-rectangle',
        'background-color': '#fb923c',
        label: 'data(label)',
        color: '#fff7ed',
        'font-size': '10px',
        'font-weight': 'bold',
        'text-valign': 'center',
        'text-halign': 'center',
        width: 82,
        height: 30,
        'border-width': 2,
        'border-color': '#ea580c',
      },
    },
    // Edges
    {
      selector: 'edge[status="concluded"]',
      style: {
        width: 2,
        'line-color': '#6ee7b7',
        'target-arrow-color': '#6ee7b7',
        'target-arrow-shape': 'triangle',
        'curve-style': 'bezier',
        label: 'data(label)',
        'font-size': '7px',
        color: '#94a3b8',
        'text-rotation': 'autorotate',
        'text-margin-y': -9,
        'text-max-width': '80px',
        'text-wrap': 'ellipsis',
        'arrow-scale': 0.9,
      },
    },
    {
      selector: 'edge[status="in_progress"]',
      style: {
        width: 2,
        'line-color': '#fbbf24',
        'line-style': 'dashed',
        'target-arrow-color': '#fbbf24',
        'target-arrow-shape': 'triangle',
        'curve-style': 'bezier',
        label: 'data(label)',
        'font-size': '7px',
        color: '#b45309',
        'text-rotation': 'autorotate',
        'text-margin-y': -9,
        'arrow-scale': 0.9,
      },
    },
    {
      selector: 'edge[status="unclaimed"]',
      style: {
        width: 1.5,
        'line-color': '#cbd5e1',
        'line-style': 'dashed',
        'target-arrow-color': '#cbd5e1',
        'target-arrow-shape': 'triangle',
        'curve-style': 'bezier',
        label: 'data(label)',
        'font-size': '7px',
        color: '#94a3b8',
        'text-rotation': 'autorotate',
        'text-margin-y': -9,
        'arrow-scale': 0.7,
      },
    },
    {
      selector: 'edge[status="bootstrap_running"]',
      style: {
        width: 2.5,
        'line-color': '#fb923c',
        'line-style': 'dashed',
        'target-arrow-color': '#fb923c',
        'target-arrow-shape': 'triangle',
        'curve-style': 'bezier',
        'arrow-scale': 0.95,
      },
    },
    {
      selector: 'edge[status="bootstrap_pending"]',
      style: {
        width: 2,
        'line-color': '#fdba74',
        'line-style': 'dashed',
        'target-arrow-color': '#fdba74',
        'target-arrow-shape': 'triangle',
        'curve-style': 'bezier',
        'arrow-scale': 0.85,
      },
    },
    {
      selector: 'edge[edgeType="bootstrap_scope"]',
      style: {
        label: '',
        width: 1.8,
        'curve-style': 'bezier',
        'line-style': 'dotted',
        'target-arrow-shape': 'triangle',
        'arrow-scale': 0.75,
      },
    },
    // Focus/highlight states
    {
      selector: '.highlight',
      style: { 'z-index': 999 },
    },
    {
      selector: 'node.focus',
      style: {
        'border-width': 3,
        'border-color': '#312e81',
        'border-opacity': 0.95,
        'z-index': 1000,
      },
    },
    {
      selector: 'edge.focus',
      style: {
        'z-index': 1000,
        'overlay-color': '#93c5fd',
        'overlay-opacity': 0.22,
        'overlay-padding': 5,
      },
    },
    {
      selector: 'node.selected-fact',
      style: {
        'underlay-color': '#93c5fd',
        'underlay-padding': 8,
        'underlay-opacity': 0.28,
        'z-index': 1001,
      },
    },
    {
      selector: '.faded',
      style: { opacity: 0.35 },
    },
  ]
}

function getLayoutOpts(mode: LayoutMode): cytoscape.LayoutOptions {
  const direction = mode.endsWith('_lr') ? 'LR' : 'TB'

  if (mode.startsWith('elk')) {
    return {
      name: 'elk',
      fit: true,
      padding: 50,
      animate: true,
      animationDuration: 350,
      elk: {
        algorithm: 'layered',
        'elk.direction': direction === 'TB' ? 'DOWN' : 'RIGHT',
        'elk.spacing.nodeNode': '50',
        'elk.layered.spacing.nodeNodeBetweenLayers': '80',
      },
    } as cytoscape.LayoutOptions
  }

  if (mode.startsWith('klay')) {
    return {
      name: 'klay',
      fit: true,
      padding: 50,
      animate: true,
      animationDuration: 400,
      klay: {
        direction: direction === 'TB' ? 'DOWN' : 'RIGHT',
        edgeRouting: 'POLYLINE',
        nodePlacement: 'BRANDES_KOEPF',
        spacing: 48,
      },
    } as cytoscape.LayoutOptions
  }

  return {
    name: 'dagre',
    rankDir: direction,
    nodeSep: 60,
    rankSep: 80,
    padding: 50,
    fit: true,
    animate: true,
    animationDuration: 400,
  } as cytoscape.LayoutOptions
}

export default function GraphView() {
  const containerRef = useRef<HTMLDivElement>(null)
  const cyRef = useRef<Core | null>(null)
  const projectRef = useRef<ProjectDetail | null>(null)
  const project = useAppStore(s => s.project)
  const layoutMode = useAppStore(s => s.layoutMode)
  const setSelectedNode = useAppStore(s => s.setSelectedNode)
  const setSelectedFacts = useAppStore(s => s.setSelectedFacts)
  const toggleFactSelection = useAppStore(s => s.toggleFactSelection)
  const clearSelection = useAppStore(s => s.clearSelection)
  const setSideTab = useAppStore(s => s.setSideTab)

  // Keep project ref in sync
  projectRef.current = project

  // Build & init graph — only depends on layoutMode, reads project from ref
  const initGraph = useCallback(() => {
    const proj = projectRef.current
    if (!containerRef.current || !proj) return
    if (cyRef.current) {
      cyRef.current.destroy()
      cyRef.current = null
    }

    const { nodes, edges } = buildElements(proj)
    const cy = cytoscape({
      container: containerRef.current,
      elements: [...nodes, ...edges],
      style: graphStyles(),
      layout: getLayoutOpts(layoutMode),
      minZoom: 0.15,
      maxZoom: 3.5,
    })

    cy.on('tap', 'node', (e: EventObject) => {
      const node = e.target
      const id = node.data('id') as string
      const nt = node.data('nodeType') as string

      if (['in_progress', 'unclaimed', 'bootstrap_pending', 'bootstrap_running'].includes(nt)) {
        const intentId = node.data('intentId') as string
        setSelectedNode({ type: 'intent', id: intentId })
        setSelectedFacts([])
        setSideTab('detail')
        if (projectRef.current) applyIntentHighlight(cy, projectRef.current, intentId)
        return
      }

      if (e.originalEvent.shiftKey) {
        toggleFactSelection(id)
      } else {
        setSelectedFacts([id])
        setSelectedNode({ type: 'fact', id })
        if (projectRef.current) applyFactHighlight(cy, projectRef.current, id)
      }
      setSideTab('detail')
    })

    cy.on('tap', 'edge', (e: EventObject) => {
      const intentId = e.target.data('intentId') as string
      if (intentId) {
        setSelectedNode({ type: 'intent', id: intentId })
        setSelectedFacts([])
        if (projectRef.current) applyIntentHighlight(cy, projectRef.current, intentId)
        setSideTab('detail')
      }
    })

    cy.on('tap', (e: EventObject) => {
      if (e.target === cy) {
        clearSelection()
        cy.elements().removeClass('highlight focus faded selected-fact')
      }
    })

    cyRef.current = cy
    pulseActive(cy)
  }, [layoutMode, setSelectedNode, setSelectedFacts, toggleFactSelection, clearSelection, setSideTab])

  // Init graph on mount or layout change
  useEffect(() => {
    if (project) initGraph()
    return () => {
      if (cyRef.current) {
        cyRef.current.destroy()
        cyRef.current = null
      }
    }
  }, [initGraph])

  // Also init when project first becomes available
  useEffect(() => {
    if (project && !cyRef.current) {
      initGraph()
    }
  }, [project, initGraph])

  // Update graph when project changes (without full reinit)
  useEffect(() => {
    if (!cyRef.current || !project) return
    const cy = cyRef.current
    const { nodes, edges } = buildElements(project)
    const wantNodes = new Set(nodes.map(n => n.data.id!))
    const wantEdges = new Set(edges.map(e => e.data.id!))
    let changed = false

    cy.nodes().forEach(n => { if (!wantNodes.has(n.id())) { n.remove(); changed = true } })
    cy.edges().forEach(e => { if (!wantEdges.has(e.id())) { e.remove(); changed = true } })

    for (const n of nodes) {
      const ex = cy.getElementById(n.data.id!)
      if (ex.length === 0) {
        cy.add(n)
        changed = true
      } else if (ex.data('nodeType') !== n.data.nodeType || ex.data('label') !== n.data.label) {
        ex.data(n.data)
        changed = true
      }
    }
    for (const e of edges) {
      const ex = cy.getElementById(e.data.id!)
      if (ex.length === 0) {
        cy.add(e)
        changed = true
      } else if (ex.data('status') !== e.data.status) {
        ex.data(e.data)
      }
    }
    if (changed) cy.layout(getLayoutOpts(layoutMode)).run()
    pulseActive(cy)
  }, [project, layoutMode])

  const handleFit = () => {
    if (cyRef.current) cyRef.current.fit(undefined, 50)
  }

  const handleLayoutChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    const mode = e.target.value as LayoutMode
    useAppStore.getState().setLayoutMode(mode)
    if (cyRef.current) {
      cyRef.current.layout(getLayoutOpts(mode)).run()
    }
  }

  return (
    <div className="relative flex-1 h-full">
      {/* Controls */}
      <div className="absolute top-3 left-3 z-10 flex items-center gap-1.5">
        <button
          onClick={handleFit}
          className="h-7 w-7 bg-white/90 backdrop-blur rounded-lg shadow-sm border border-slate-200/60 text-slate-500 hover:text-slate-700 hover:bg-white transition inline-flex items-center justify-center"
          title="Fit graph"
        >
          <svg className="w-4 h-4" fill="none" stroke="currentColor" strokeWidth="1.7" viewBox="0 0 24 24">
            <path d="M8 3H5a2 2 0 0 0-2 2v3" /><path d="M16 3h3a2 2 0 0 1 2 2v3" />
            <path d="M8 21H5a2 2 0 0 1-2-2v-3" /><path d="M16 21h3a2 2 0 0 0 2-2v-3" />
          </svg>
        </button>
        <select
          value={layoutMode}
          onChange={handleLayoutChange}
          className="h-7 bg-white/90 backdrop-blur rounded-lg shadow-sm border border-slate-200/60 px-2.5 text-xs text-slate-600 hover:bg-white focus:outline-none transition"
        >
          <option value="dagre_tb">Dagre ↓</option>
          <option value="dagre_lr">Dagre →</option>
          <option value="klay_tb">Klay ↓</option>
          <option value="klay_lr">Klay →</option>
          <option value="elk_tb">ELK ↓</option>
          <option value="elk_lr">ELK →</option>
        </select>
      </div>

      {/* Reason indicator */}
      {project?.project.reason && (
        <div className="absolute top-14 right-3 z-10 max-w-80">
          <div className="relative overflow-hidden rounded-2xl border border-sky-200/80 bg-white/92 px-3.5 py-3 shadow-lg backdrop-blur animate-pulse">
            <div className="flex items-start gap-3">
              <span className="mt-1 w-2.5 h-2.5 rounded-full bg-sky-500 animate-ping" />
              <div>
                <div className="text-[10px] font-semibold uppercase tracking-widest text-sky-500">Reason Running</div>
                <div className="mt-0.5 text-sm font-semibold text-slate-700">{project.project.reason.worker}</div>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Graph container */}
      <div ref={containerRef} className="w-full h-full" />
    </div>
  )
}

// --- Highlight helpers (matching Cairn's lineage logic) ---

function collectFactLineage(project: ProjectDetail, fid: string) {
  const upstreamFacts = new Set<string>()
  const upstreamIntents = new Set<string>()

  const walkFact = (factId: string) => {
    if (upstreamFacts.has(factId)) return
    upstreamFacts.add(factId)
    for (const intent of project.intents) {
      if (intent.to === factId) walkIntent(intent.id)
    }
  }
  const walkIntent = (iid: string) => {
    if (upstreamIntents.has(iid)) return
    upstreamIntents.add(iid)
    const intent = project.intents.find(i => i.id === iid)
    if (intent) for (const src of intent.from) walkFact(src)
  }

  walkFact(fid)
  return { upstreamFacts, upstreamIntents }
}

function applyFactHighlight(cy: Core, project: ProjectDetail, factId: string) {
  cy.elements().removeClass('highlight focus faded selected-fact')
  const { upstreamFacts, upstreamIntents } = collectFactLineage(project, factId)

  const nodeIds = new Set(upstreamFacts)
  const edgeIds = new Set<string>()
  for (const iid of upstreamIntents) {
    const intent = project.intents.find(i => i.id === iid)
    if (!intent) continue
    if (intent.to) nodeIds.add(intent.to)
    else nodeIds.add(`_ph_${intent.id}`)
    for (const src of intent.from) edgeIds.add(`${intent.id}_${src}`)
  }

  const hlNodes = cy.nodes().filter(n => nodeIds.has(n.id()))
  const hlEdges = cy.edges().filter(e => edgeIds.has(e.id()))
  const focusNode = cy.getElementById(factId)

  hlNodes.addClass('highlight')
  hlEdges.addClass('highlight')
  focusNode.addClass('focus')
  cy.elements().not(hlNodes.add(hlEdges).add(focusNode)).addClass('faded')
}

function applyIntentHighlight(cy: Core, project: ProjectDetail, intentId: string) {
  cy.elements().removeClass('highlight focus faded selected-fact')
  const intent = project.intents.find(i => i.id === intentId)
  if (!intent) return

  const nodeIds = new Set<string>()
  const edgeIds = new Set<string>()
  if (intent.to) nodeIds.add(intent.to)
  else nodeIds.add(`_ph_${intent.id}`)
  for (const src of intent.from) {
    nodeIds.add(src)
    edgeIds.add(`${intent.id}_${src}`)
  }

  const hlNodes = cy.nodes().filter(n => nodeIds.has(n.id()))
  const focusEdges = cy.edges().filter(e => edgeIds.has(e.id()))
  hlNodes.addClass('highlight')
  focusEdges.addClass('focus')
  cy.elements().not(hlNodes.add(focusEdges)).addClass('faded')
}

function pulseActive(cy: Core) {
  cy.nodes('[nodeType="in_progress"], [nodeType="bootstrap_running"]').forEach(node => {
    if (node.scratch('_pulse')) return
    node.scratch('_pulse', true)
    const loop = () => {
      if (!node.inside()) return
      node.animate(
        { style: { 'background-opacity': 0.4 } },
        { duration: 900, complete: () => {
          if (!node.inside()) return
          node.animate(
            { style: { 'background-opacity': 0.9 } },
            { duration: 900, complete: loop }
          )
        }}
      )
    }
    loop()
  })
}
