import { describe, expect, it, vi } from 'vitest'
import * as THREE from 'three'
import type { TopologyEdge, TopologyNode } from '../api/topologyTypes'
import type { SpatialPlacement, SpatialSceneLayout } from './spatialScene'
import { buildSpatialObjects, createSpatialThreeRenderer, disposeSpatialObjects, networkRoleColor, setSpatialSelection, spatialObjectLabel } from './spatialThreeRenderer'

const runtime = vi.hoisted(() => ({ scene: undefined as unknown, camera: undefined as unknown, canvas: undefined as unknown, dispose: vi.fn(), controlsDispose: vi.fn(), contextLoss: vi.fn() }))
vi.mock('three', async importOriginal => {
  const actual = await importOriginal<typeof import('three')>()
  return { ...actual, WebGLRenderer: class {
    domElement = Object.assign(new EventTarget(), { getBoundingClientRect: () => ({ left: 0, top: 0, width: 800, height: 600 }), remove: vi.fn() })
    constructor() { runtime.canvas = this.domElement }
    setPixelRatio() {}
    setClearColor() {}
    setSize() {}
    render(scene: THREE.Scene, camera: THREE.Camera) { runtime.scene = scene; runtime.camera = camera; scene.updateMatrixWorld(true); camera.updateMatrixWorld(true) }
    dispose = runtime.dispose
    forceContextLoss = runtime.contextLoss
  } }
})
vi.mock('three/addons/controls/OrbitControls.js', () => ({ OrbitControls: class {
  target = new THREE.Vector3()
  constructor(private camera: THREE.Camera) {}
  update() { this.camera.lookAt(this.target); this.camera.updateMatrixWorld(true) }
  dispose = runtime.controlsDispose
} }))

function placement(id: string, kind: SpatialPlacement['kind'], x = 0, y = 0, z = 0, parentId?: string): SpatialPlacement {
  const node: TopologyNode = { id, label: id, entity_type: kind === 'cabinet' ? 'cabinet' : kind === 'gpu' ? 'gpu' : 'device', source_id: 'inventory', stable_id: id, properties: {}, created_at: null, synced_at: null }
  return { node, kind, x, y, z, parentId, role: 'T1' }
}
function edge(id: string, from: string, to: string, edge_type: TopologyEdge['edge_type'] = 'network_relation'): TopologyEdge {
  return { id, from, to, edge_type, relation_kind: 'links_to', rank: '', source_id: 'inventory', properties: {}, created_at: null, synced_at: null }
}
function layout(placements: SpatialPlacement[], edges: TopologyEdge[] = []): SpatialSceneLayout {
  return { placements, edges, warnings: [], width: 20, height: 10, depth: 20 }
}

function rendererFixture() {
  const callbacks: FrameRequestCallback[] = []
  const view = Object.assign(new EventTarget(), {
    devicePixelRatio: 1,
    requestAnimationFrame(callback: FrameRequestCallback) { callbacks.push(callback); return callbacks.length },
    cancelAnimationFrame: vi.fn(),
    ResizeObserver: class { observe() {} disconnect() {} },
  })
  const document = { defaultView: view, createElement: () => ({ width: 0, height: 0, getContext: () => ({ fillRect() {}, fillText() {} }) }) }
  const container = { ownerDocument: document, clientWidth: 800, clientHeight: 600, appendChild: vi.fn() } as unknown as HTMLElement
  const selected = vi.fn()
  return { renderer: createSpatialThreeRenderer(container, selected), callbacks, selected }
}

describe('spatial Three.js lifecycle', () => {
  it('labels real network equipment by role and SN, not GPU', () => {
    const n = placement('210235A53L5264L100BY', 'network', 0, 8, 0)
    n.node.properties.parent_type_id = 54; n.role = 'T0'; n.node.label = 'ZQZ11_F403-F-02_POD249_T0_CLD_047-GDR'
    expect(spatialObjectLabel(n)).toContain('网络设备 T0')
    expect(spatialObjectLabel(n)).toContain(n.node.stable_id)
    expect(spatialObjectLabel(n)).not.toContain('GPU')
  })
  it('rebuilds actual nodes, uses literal canvas labels, picks IDs, ignores drags, and releases runtime resources', () => {
    const callbacks: FrameRequestCallback[] = []
    const cancelFrame = vi.fn()
    const disconnect = vi.fn()
    const fillText = vi.fn()
    const view = Object.assign(new EventTarget(), {
      devicePixelRatio: 1,
      requestAnimationFrame(callback: FrameRequestCallback) { callbacks.push(callback); return callbacks.length },
      cancelAnimationFrame: cancelFrame,
      ResizeObserver: class { observe() {} disconnect = disconnect },
    })
    const document = { defaultView: view, createElement: () => ({ width: 0, height: 0, getContext: () => ({ fillRect() {}, fillText }) }) }
    const container = { ownerDocument: document, clientWidth: 800, clientHeight: 600, appendChild: vi.fn() } as unknown as HTMLElement
    const selected = vi.fn()
    const renderer = createSpatialThreeRenderer(container, selected)
    const item = placement('server-real', 'device', 0, 1, 0, 'rack')
    item.node.label = '<img src=x onerror=alert(1)>'
    renderer.update(layout([placement('rack', 'cabinet'), item]), 'space')
    renderer.select(item.node.id)
    callbacks[0]!(0)
    const scene = runtime.scene as THREE.Scene
    let box!: THREE.Mesh
    scene.traverse(value => { if (value instanceof THREE.Mesh && value.userData.id === item.node.id) box = value })
    const geometryDispose = vi.spyOn(box.geometry, 'dispose')
    expect(fillText).toHaveBeenCalledWith(item.node.label, 256, 40, 490)
    const camera = runtime.camera as THREE.PerspectiveCamera
    const screen = new THREE.Vector3(item.x, item.y, item.z).project(camera)
    const canvas = runtime.canvas as EventTarget
    function pointer(type: string, extra = 0) {
      canvas.dispatchEvent(Object.assign(new Event(type), { button: 0, pointerId: 1, clientX: (screen.x + 1) * 400 + extra, clientY: (1 - screen.y) * 300 }))
    }
    pointer('pointerdown'); pointer('pointerup')
    expect(selected).toHaveBeenCalledWith('server-real')
    selected.mockClear()
    pointer('pointerdown'); pointer('pointermove', 12); pointer('pointerup')
    expect(selected).not.toHaveBeenCalled()
    renderer.update(layout([placement('second-root', 'network')]), 'network')
    expect(geometryDispose).toHaveBeenCalledOnce()
    renderer.zoom(1.25)
    renderer.reset()
    renderer.dispose()
    renderer.dispose()
    expect(scene.children).toHaveLength(0)
    expect(disconnect).toHaveBeenCalledOnce()
    expect(cancelFrame).toHaveBeenCalledOnce()
    expect(runtime.dispose).toHaveBeenCalledOnce()
    expect(runtime.controlsDispose).toHaveBeenCalledOnce()
    expect(runtime.contextLoss).toHaveBeenCalledOnce()
  })
})

describe('spatial Three.js review regressions', () => {
  it('picks devices through transparent rack bodies while keeping rack labels selectable', () => {
    const { renderer, callbacks, selected } = rendererFixture()
    renderer.update(layout([placement('rack', 'cabinet'), placement('server', 'device', 0, 1, 0, 'rack')]), 'space')
    callbacks[0]!(0)
    const scene = runtime.scene as THREE.Scene
    const camera = runtime.camera as THREE.PerspectiveCamera
    const root = scene.children.find(child => child instanceof THREE.Group && child.children.some(value => value.userData.id === 'rack'))!
    const rack = root.children.find(child => child.userData.id === 'rack')!
    const rackMesh = rack.children.find(child => child instanceof THREE.Mesh)!
    const rackLabel = rack.children.find(child => child instanceof THREE.Sprite)!
    const raycaster = new THREE.Raycaster(new THREE.Vector3(0, 1, 10), new THREE.Vector3(0, 0, -1))
    expect(raycaster.intersectObject(rackMesh)).toHaveLength(0)
    function clickAt(point: THREE.Vector3) {
      const screen = point.project(camera)
      for (const type of ['pointerdown', 'pointerup']) {
        (runtime.canvas as EventTarget).dispatchEvent(Object.assign(new Event(type), {
          button: 0, pointerId: 1, clientX: (screen.x + 1) * 400, clientY: (1 - screen.y) * 300,
        }))
      }
    }
    clickAt(new THREE.Vector3(0, 1, 0))
    expect(selected).toHaveBeenLastCalledWith('server')
    clickAt(rackLabel.getWorldPosition(new THREE.Vector3()))
    expect(selected).toHaveBeenLastCalledWith('rack')
    renderer.dispose()
  })

  it('groups tier planes only from real network devices, not interfaces or lookalike roles', () => {
    const { renderer, callbacks } = rendererFixture()
    const t2 = placement('t2', 'network', 0, 24)
    t2.role = 'T2'; t2.node.properties.parent_type_id = 54
    const t0 = placement('t0', 'network', 5, 8)
    t0.role = 'T0'; t0.node.properties.parent_type_id = '54'
    const unknown = placement('unknown', 'network', 10, 4)
    unknown.role = 'customT2'; unknown.node.properties.parent_type_id = 54
    const port = placement('port', 'network', 1.5, 24, 0, 't2')
    port.node.entity_type = 'interface'; port.role = ''; port.node.properties.parent_type_id = 54
    const impostor = placement('server', 'network', 15, 16)
    impostor.node.properties.parent_type_id = 1
    renderer.update(layout([t2, t0, unknown, port, impostor]), 'network')
    callbacks[0]!(0)
    const planes: THREE.Mesh[] = []
    ;(runtime.scene as THREE.Scene).traverse(object => {
      if (object instanceof THREE.Mesh && object.geometry instanceof THREE.PlaneGeometry) planes.push(object)
    })
    expect(planes.map(plane => plane.position.y).sort((a, b) => a - b)).toEqual([3.7, 7.7, 23.7])
    renderer.dispose()
  })
})

describe('spatial Three.js objects', () => {
  it('colors only recognized network roles and does not infer a tier from arbitrary names', () => {
    expect(networkRoleColor('T2')).toBe('#a78bfa')
    expect(networkRoleColor('t1')).toBe('#60a5fa')
    expect(networkRoleColor('T0')).toBe('#2dd4bf')
    expect(networkRoleColor('unknown')).toBe('#94a3b8')
    expect(networkRoleColor('serverT2extra')).toBe('#94a3b8')
  })

  it('parents only explicit containment while preserving supplied world coordinates', () => {
    const scene = buildSpatialObjects(layout([placement('rack', 'cabinet', 4, 2, 6), placement('server', 'device', 4, 3, 6, 'rack'), placement('gpu', 'gpu', 4.2, 3.1, 6, 'server'), placement('loose', 'device', 8, 1, 2, 'missing')]), 'space')
    scene.root.updateMatrixWorld(true)
    expect(scene.nodes.size).toBe(4)
    expect(scene.nodes.get('server')?.parent).toBe(scene.nodes.get('rack'))
    expect(scene.nodes.get('gpu')?.getWorldPosition(new THREE.Vector3()).toArray()).toEqual([4.2, 3.1, 6])
    expect(scene.nodes.get('loose')?.parent).toBe(scene.root)
    disposeSpatialObjects(scene.root)
  })

  it('anchors cabinet frames at their floor placement and fits only loaded child slots', () => {
    const scene = buildSpatialObjects(layout([placement('rack', 'cabinet', 0, 0, 0), placement('server', 'device', 0, 8, 0, 'rack')]), 'space')
    scene.root.updateMatrixWorld(true)
    const rack = scene.nodes.get('rack')!.children.find(child => child instanceof THREE.Mesh) as THREE.Mesh
    const bounds = new THREE.Box3().setFromObject(rack)
    expect(bounds.min.y).toBe(0)
    expect(bounds.max.y).toBeGreaterThan(8)
    expect(scene.nodes.size).toBe(2)
    disposeSpatialObjects(scene.root)
  })

  it('draws only real network edges with two rendered endpoints, without parent substitutions', () => {
    const scene = buildSpatialObjects(layout([placement('rack', 'cabinet'), placement('a', 'network', 1, 2, 3), placement('b', 'device', 5, 6, 7)], [edge('real', 'a', 'b'), edge('absent-port', 'unknown-port', 'b'), edge('containment', 'rack', 'a', 'composition_relation')]), 'network')
    expect(scene.edgeCount).toBe(1)
    const line = scene.root.children.find(child => child.userData.edgeId === 'real') as THREE.Line
    expect(Array.from(line.geometry.getAttribute('position').array)).toEqual([1, 2, 3, 3, 6.5, 5, 5, 6, 7])
    disposeSpatialObjects(scene.root)
  })

  it('highlights only the selected object and restores original materials on reset', () => {
    const scene = buildSpatialObjects(layout([placement('a', 'device'), placement('b', 'device')]), 'space')
    const a = scene.nodes.get('a')!.children.find(child => child instanceof THREE.Mesh) as THREE.Mesh<THREE.BoxGeometry, THREE.MeshStandardMaterial>
    const original = a.material.color.getHex()
    setSpatialSelection(scene.root, 'a')
    expect(a.material.emissive.getHex()).not.toBe(0)
    setSpatialSelection(scene.root, 'b')
    expect(a.material.emissive.getHex()).toBe(0)
    expect(a.material.color.getHex()).toBe(original)
    setSpatialSelection(scene.root, null)
    disposeSpatialObjects(scene.root)
  })

  it('disposes each shared geometry, material, and texture only once', () => {
    const root = new THREE.Group()
    const geometry = new THREE.BoxGeometry()
    const texture = new THREE.Texture()
    const material = new THREE.MeshStandardMaterial({ map: texture })
    root.add(new THREE.Mesh(geometry, material), new THREE.Mesh(geometry, material))
    const geometryDispose = vi.spyOn(geometry, 'dispose')
    const materialDispose = vi.spyOn(material, 'dispose')
    const textureDispose = vi.spyOn(texture, 'dispose')
    disposeSpatialObjects(root)
    expect(geometryDispose).toHaveBeenCalledTimes(1)
    expect(materialDispose).toHaveBeenCalledTimes(1)
    expect(textureDispose).toHaveBeenCalledTimes(1)
    expect(root.children).toHaveLength(0)
  })
})
