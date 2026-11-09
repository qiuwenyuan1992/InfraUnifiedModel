import { effectScope, nextTick, reactive } from 'vue'
import { afterEach, expect, it, vi } from 'vitest'
import type { Core, CytoscapeOptions } from 'cytoscape'
import TopologyCanvas from './TopologyCanvas.vue'
import type { TopologyEdge, TopologyGraph, TopologyNode, TopologySelection } from '../api/topologyTypes'

const hooks = vi.hoisted(() => ({ mount: () => {}, unmount: () => {}, cores: [] as Core[] }))
vi.mock('vue', async importOriginal => {
  const actual = await importOriginal<typeof import('vue')>()
  return { ...actual, onMounted: (hook: () => void) => { hooks.mount = hook }, onBeforeUnmount: (hook: () => void) => { hooks.unmount = hook }, useSSRContext: () => ({ modules: new Set<string>() }) }
})
vi.mock('cytoscape', async importOriginal => {
  const actual = await importOriginal<{ default: (options: CytoscapeOptions) => Core }>()
  return { default: (options: CytoscapeOptions) => {
    const core = actual.default({ ...options, container: undefined, headless: true, styleEnabled: true })
    hooks.cores.push(core)
    return core
  } }
})
afterEach(() => { hooks.cores.splice(0).forEach(core => { if (!core.destroyed()) core.destroy() }); vi.unstubAllGlobals() })
const node = (id: string): TopologyNode => ({ id, source_id: 's', entity_type: 'device', stable_id: id, label: id, properties: {}, created_at: null, synced_at: null })
const edge = (id: string): TopologyEdge => ({ id, source_id: 's', edge_type: 'network_relation', relation_kind: 'links_to', from: 'a', to: 'b', rank: '0', properties: {}, created_at: null, synced_at: null })

it('labels real A/B parallel power paths, UPS side branches and POD plane without changing edge identity', () => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  const edges = [
    { ...edge('A'), edge_type: 'power_relation' as const, relation_kind: 'power_upstream', properties: { power_path: 'A' } },
    { ...edge('B'), edge_type: 'power_relation' as const, relation_kind: 'power_upstream', properties: { power_path: 'B' } },
    { ...edge('UPS'), edge_type: 'power_relation' as const, relation_kind: 'member_of' },
    { ...edge('plane'), edge_type: 'spatial_relation' as const, relation_kind: 'member_of', properties: { plane: 2 } },
  ]
  const props = reactive({ graph: { nodes: [node('a'), node('b')], edges }, selection: null, rootId: 'a' })
  const scope = effectScope()
  const setup = (TopologyCanvas as unknown as { setup: (props: unknown, context: unknown) => unknown }).setup
  scope.run(() => setup(props, { expose: () => {}, emit: () => {} }))
  hooks.mount()
  const cy = hooks.cores[0]!
  try {
    expect(cy.getElementById('edge:A').data('label')).toContain('A')
    expect(cy.getElementById('edge:B').data('label')).toContain('B')
    expect(cy.getElementById('edge:UPS').data('label')).toContain('UPS 成员')
    expect(cy.getElementById('edge:plane').data('label')).toContain('计算平面')
    expect(cy.edges()).toHaveLength(4)
  } finally { hooks.unmount(); scope.stop() }
})

it('preserves moved positions, actual parallel IDs, selection events, and destroys its canvas/observer', async () => {
  const disconnect = vi.fn()
  let resize!: () => void
  vi.stubGlobal('ResizeObserver', class {
    constructor(callback: () => void) { resize = callback }
    observe = vi.fn()
    disconnect = disconnect
  })
  const props = reactive<{ graph: TopologyGraph; selection: TopologySelection | null; rootId: string }>({ graph: { nodes: [node('a'), { ...node('b'), entity_type: 'gpu', stable_id: '1750125024470', label: '2335-895-A1' }], edges: [edge('parallel-a'), edge('parallel-b')] }, selection: { kind: 'node', id: 'a' }, rootId: 'a' })
  const scope = effectScope()
  const emit = vi.fn()
  const setup = (TopologyCanvas as unknown as { setup: (props: unknown, context: unknown) => unknown }).setup
  scope.run(() => setup(props, { expose: () => {}, emit }))
  hooks.mount()
  const cy = hooks.cores[0]!
  try {
    expect(cy.nodes()).toHaveLength(2)
    expect(cy.getElementById('node:b').data('label')).toContain('1750125024470')
    expect(cy.getElementById('node:b').data('label')).toContain('GPU')
    expect(cy.getElementById('node:b').data('label')).toContain('2335-895-A1')
    expect(cy.edges().map(item => item.id())).toEqual(['edge:parallel-a', 'edge:parallel-b'])
    cy.getElementById('node:a').position({ x: 70, y: 80 })
    props.graph = { nodes: [node('a'), node('b'), node('c')], edges: [...props.graph.edges] }
    await nextTick()
    expect(cy.getElementById('node:a').position()).toEqual({ x: 70, y: 80 })
    expect(cy.nodes()).toHaveLength(3)
    expect(cy.getElementById('node:a').selected()).toBe(true)
    cy.getElementById('edge:parallel-b').emit('tap')
    expect(emit).toHaveBeenCalledWith('select', { kind: 'edge', id: 'parallel-b' })
    cy.getElementById('node:b').emit('tap')
    expect(emit).toHaveBeenCalledWith('select', { kind: 'node', id: 'b' })
    resize()
  } finally { hooks.unmount(); scope.stop() }
  expect(cy.destroyed()).toBe(true)
  expect(disconnect).toHaveBeenCalledOnce()
  expect(() => resize()).not.toThrow()
})
