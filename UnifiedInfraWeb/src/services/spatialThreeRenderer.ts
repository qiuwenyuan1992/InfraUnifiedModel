import * as THREE from 'three'
import { OrbitControls } from 'three/addons/controls/OrbitControls.js'
import type { SpatialPlacement, SpatialSceneLayout } from './spatialScene'

export type SpatialMode = 'space' | 'network'
export function networkRoleColor(role: string): string {
  return ({ T2: '#a78bfa', T1: '#60a5fa', T0: '#2dd4bf' } as Record<string, string>)[role.trim().toUpperCase()] || '#94a3b8'
}
export function spatialObjectLabel(placement: SpatialPlacement): string {
  if (placement.kind === 'network' && placement.node.entity_type === 'device') return `网络设备 ${placement.role || '未分类'} · ${placement.node.stable_id}`
  if (placement.node.entity_type === 'gpu') return `GPU卡 · ${placement.node.stable_id}`
  if (placement.node.entity_type === 'interface') return `端口 · ${placement.node.label || placement.node.stable_id}`
  return placement.node.label || placement.node.stable_id
}
function dimensions(placement: SpatialPlacement): [number, number, number] {
  if (placement.kind === 'cabinet') return [6.8, 6, 4.8]
  if (placement.kind === 'gpu') return [0.35, 0.18, 0.35]
  if (placement.node.entity_type === 'interface') return [0.18, 0.18, 0.18]
  if (placement.kind === 'background') return [1.8, 0.15, 1.6]
  return placement.kind === 'network' ? [1.35, 0.4, 1.1] : [6.4, 0.55, 4.1]
}
export function buildSpatialObjects(layout: SpatialSceneLayout, mode: SpatialMode) {
  const root = new THREE.Group()
  const nodes = new Map<string, THREE.Group>()
  const placements = new Map(layout.placements.map(placement => [placement.node.id, placement]))
  const rackHeights = new Map<string, number>()
  for (const placement of placements.values()) {
    const parent = placement.parentId ? placements.get(placement.parentId) : undefined
    if (parent?.kind === 'cabinet' && (placement.kind === 'device' || (mode === 'space' && placement.kind === 'network'))) {
      rackHeights.set(parent.node.id, Math.max(rackHeights.get(parent.node.id) || 6, placement.y - parent.y + 0.75))
    }
  }
  for (const placement of placements.values()) {
    const group = new THREE.Group()
    group.userData.id = placement.node.id
    group.position.set(placement.x, placement.y, placement.z)
    const color = placement.kind === 'network' ? networkRoleColor(placement.role) : placement.kind === 'gpu' ? '#4ade80' : placement.kind === 'cabinet' ? '#28496b' : placement.kind === 'background' ? '#314257' : '#4396e8'
    const size = dimensions(placement)
    if (placement.kind === 'cabinet') size[1] = rackHeights.get(placement.node.id) || size[1]
    group.userData.height = size[1]
    const geometry = new THREE.BoxGeometry(...size)
    if (placement.kind === 'cabinet') geometry.translate(0, size[1] / 2, 0)
    const mesh = new THREE.Mesh(geometry, new THREE.MeshStandardMaterial({ color, roughness: 0.55, metalness: 0.25, transparent: placement.kind === 'cabinet' || placement.kind === 'background', opacity: placement.kind === 'cabinet' ? 0.12 : placement.kind === 'background' ? 0.2 : 0.95, depthWrite: placement.kind !== 'cabinet' && placement.kind !== 'background' }))
    mesh.userData.id = placement.node.id
    mesh.userData.pickable = placement.kind !== 'cabinet'
    if (placement.kind === 'cabinet') mesh.raycast = () => {}
    group.add(mesh)
    const outline = new THREE.LineSegments(new THREE.EdgesGeometry(geometry), new THREE.LineBasicMaterial({ color: placement.kind === 'cabinet' ? '#72baff' : color, transparent: true, opacity: 0.85 }))
    outline.userData.id = placement.node.id
    group.add(outline)
    nodes.set(placement.node.id, group)
    root.add(group)
  }
  for (const placement of placements.values()) {
    const group = nodes.get(placement.node.id)!
    const parent = placement.parentId ? nodes.get(placement.parentId) : undefined
    if (!parent || parent === group) continue
    let ancestor: THREE.Object3D | null = parent
    while (ancestor && ancestor !== group) ancestor = ancestor.parent
    if (ancestor === group) continue
    const parentPlacement = placements.get(placement.parentId!)!
    parent.add(group)
    group.position.set(placement.x - parentPlacement.x, placement.y - parentPlacement.y, placement.z - parentPlacement.z)
  }
  let edgeCount = 0
  for (const edge of layout.edges) {
    if (edge.edge_type !== 'network_relation') continue
    const from = placements.get(edge.from)
    const to = placements.get(edge.to)
    if (!from || !to) continue
    const lift = mode === 'network' ? 0.5 : 0.25
    const geometry = new THREE.BufferGeometry().setFromPoints([new THREE.Vector3(from.x, from.y, from.z), new THREE.Vector3((from.x + to.x) / 2, Math.max(from.y, to.y) + lift, (from.z + to.z) / 2), new THREE.Vector3(to.x, to.y, to.z)])
    const color = edge.relation_kind === 'gpu_uplink' ? '#4ade80' : edge.relation_kind === 'server_uplink' ? '#60a5fa' : '#67e8f9'
    const line = new THREE.Line(geometry, new THREE.LineBasicMaterial({ color, transparent: true, opacity: 0.65 }))
    line.userData.edgeId = edge.id
    root.add(line)
    edgeCount++
  }
  return { root, nodes, edgeCount }
}
export function setSpatialSelection(root: THREE.Object3D, id: string | null) {
  root.traverse(object => {
    if (!(object instanceof THREE.Mesh || object instanceof THREE.LineSegments) || !object.userData.id) return
    const selected = object.userData.id === id
    const materials = Array.isArray(object.material) ? object.material : [object.material]
    for (const material of materials) {
      if (material instanceof THREE.MeshStandardMaterial) material.emissive.set(selected ? '#27618c' : '#000000')
      if (material instanceof THREE.LineBasicMaterial) {
        material.userData.baseColor ??= material.color.getHex()
        material.color.set(selected ? '#fbbf24' : material.userData.baseColor)
      }
    }
  })
}
export function disposeSpatialObjects(root: THREE.Object3D) {
  const geometries = new Set<THREE.BufferGeometry>()
  const materials = new Set<THREE.Material>()
  const textures = new Set<THREE.Texture>()
  root.traverse(object => {
    if (!(object instanceof THREE.Mesh || object instanceof THREE.Line || object instanceof THREE.Sprite)) return
    if ('geometry' in object) geometries.add(object.geometry)
    for (const material of Array.isArray(object.material) ? object.material : [object.material]) {
      materials.add(material)
      for (const value of Object.values(material)) if (value instanceof THREE.Texture) textures.add(value)
    }
  })
  textures.forEach(texture => texture.dispose())
  materials.forEach(material => material.dispose())
  geometries.forEach(geometry => geometry.dispose())
  root.clear()
}

function labelSprite(document: Document, text: string, color = '#d7e9ff') {
  const canvas = document.createElement('canvas')
  canvas.width = 512
  canvas.height = 80
  const context = canvas.getContext('2d')
  if (!context) return undefined
  context.fillStyle = 'rgba(11,21,39,0.82)'
  context.fillRect(0, 0, canvas.width, canvas.height)
  context.fillStyle = color
  context.font = '28px sans-serif'
  context.textAlign = 'center'
  context.textBaseline = 'middle'
  context.fillText(text, 256, 40, 490)
  const texture = new THREE.CanvasTexture(canvas)
  texture.colorSpace = THREE.SRGBColorSpace
  const sprite = new THREE.Sprite(new THREE.SpriteMaterial({ map: texture, transparent: true, depthWrite: false }))
  sprite.scale.set(2.2, 0.34, 1)
  return sprite
}

export function createSpatialThreeRenderer(container: HTMLElement, onSelect: (id: string) => void) {
  const view = container.ownerDocument.defaultView
  if (!view) throw new Error('当前环境不支持 3D 画布。')
  const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: false })
  renderer.setPixelRatio(Math.min(view.devicePixelRatio || 1, 2))
  renderer.setClearColor('#0b1527')
  renderer.outputColorSpace = THREE.SRGBColorSpace
  const scene = new THREE.Scene()
  const camera = new THREE.PerspectiveCamera(45, 1, 0.1, 2000)
  const controls = new OrbitControls(camera, renderer.domElement)
  controls.enableDamping = true
  controls.dampingFactor = 0.08
  controls.maxPolarAngle = Math.PI * 0.49
  scene.add(new THREE.HemisphereLight('#c4e5ff', '#142238', 2))
  const light = new THREE.DirectionalLight('#b6d8ff', 3)
  light.position.set(10, 24, 14)
  scene.add(light)
  let objects: ReturnType<typeof buildSpatialObjects> | undefined
  let decoration = new THREE.Group()
  let bounds = new THREE.Box3(new THREE.Vector3(-5, 0, -5), new THREE.Vector3(5, 5, 5))
  let selection: string | null = null
  let frame = 0
  let disposed = false
  const raycaster = new THREE.Raycaster()
  raycaster.params.Line.threshold = 0.12
  const pointer = new THREE.Vector2()
  let down: { id: number; x: number; y: number; dragged: boolean } | undefined
  function pointerDown(event: PointerEvent) {
    if (down) { down.dragged = true; return }
    if (event.button === 0) down = { id: event.pointerId, x: event.clientX, y: event.clientY, dragged: false }
  }
  function pointerMove(event: PointerEvent) {
    if (down && Math.hypot(event.clientX - down.x, event.clientY - down.y) > 5) down.dragged = true
  }
  function pointerUp(event: PointerEvent) {
    if (!down || down.id !== event.pointerId) return
    const click = !down.dragged && Math.hypot(event.clientX - down.x, event.clientY - down.y) <= 5
    down = undefined
    if (!click || !objects) return
    const rect = renderer.domElement.getBoundingClientRect()
    if (!rect.width || !rect.height) return
    pointer.set((event.clientX - rect.left) / rect.width * 2 - 1, -(event.clientY - rect.top) / rect.height * 2 + 1)
    scene.updateMatrixWorld(true)
    camera.updateMatrixWorld(true)
    raycaster.setFromCamera(pointer, camera)
    for (const hit of raycaster.intersectObjects(objects.root.children, true)) {
      if (hit.object.userData.pickable === false) continue
      if (typeof hit.object.userData.id === 'string') { onSelect(hit.object.userData.id); break }
    }
  }
  function pointerCancel() { down = undefined }
  function resize() {
    const width = Math.max(container.clientWidth, 1)
    const height = Math.max(container.clientHeight, 1)
    renderer.setSize(width, height)
    camera.aspect = width / height
    camera.updateProjectionMatrix()
  }
  function reset() {
    const sphere = bounds.getBoundingSphere(new THREE.Sphere())
    const angle = Math.min(camera.fov * Math.PI / 360, Math.atan(Math.tan(camera.fov * Math.PI / 360) * camera.aspect))
    const distance = Math.max(sphere.radius, 2) / Math.sin(angle) * 1.12
    controls.target.copy(sphere.center)
    camera.position.copy(sphere.center).add(new THREE.Vector3(1, 0.85, 1).normalize().multiplyScalar(distance))
    camera.near = Math.max(distance / 1000, 0.01)
    camera.far = distance * 100
    camera.updateProjectionMatrix()
    controls.minDistance = Math.max(sphere.radius * 0.12, 0.3)
    controls.maxDistance = distance * 5
    controls.update()
  }
  function update(layout: SpatialSceneLayout, mode: SpatialMode) {
    if (objects) { scene.remove(objects.root); disposeSpatialObjects(objects.root) }
    scene.remove(decoration)
    disposeSpatialObjects(decoration)
    objects = buildSpatialObjects(layout, mode)
    scene.add(objects.root)
    bounds = new THREE.Box3().setFromObject(objects.root)
    if (bounds.isEmpty()) bounds.set(new THREE.Vector3(-5, 0, -5), new THREE.Vector3(5, 5, 5))
    const center = bounds.getCenter(new THREE.Vector3())
    const size = bounds.getSize(new THREE.Vector3())
    const radius = Math.max(Math.hypot(size.x, size.z) * 0.6, 6)
    const floorY = Math.min(bounds.min.y - 0.2, -0.2)
    decoration = new THREE.Group()
    const base = new THREE.Mesh(new THREE.CylinderGeometry(radius, radius, 0.12, 64), new THREE.MeshStandardMaterial({ color: '#10253f', roughness: 0.8 }))
    base.position.set(center.x, floorY - 0.06, center.z)
    decoration.add(base)
    const grid = new THREE.GridHelper(radius * 2, 32, '#5b9dd6', '#294c72')
    grid.position.set(center.x, floorY + 0.01, center.z)
    const gridMaterials = Array.isArray(grid.material) ? grid.material : [grid.material]
    gridMaterials.forEach(material => { material.transparent = true; material.opacity = 0.4 })
    decoration.add(grid)
    const ringPoints = Array.from({ length: 96 }, (_, i) => new THREE.Vector3(center.x + Math.cos(i / 96 * Math.PI * 2) * radius, floorY + 0.02, center.z + Math.sin(i / 96 * Math.PI * 2) * radius))
    decoration.add(new THREE.LineLoop(new THREE.BufferGeometry().setFromPoints(ringPoints), new THREE.LineBasicMaterial({ color: '#76baff' })))
    for (const placement of layout.placements) {
      const group = objects.nodes.get(placement.node.id)
      if (!group) continue
      const label = labelSprite(container.ownerDocument, spatialObjectLabel(placement))
      if (label) {
        label.position.y = group.userData.height * (placement.kind === 'cabinet' ? 1 : 0.5) + 0.3
        label.userData.id = placement.node.id
        if (placement.kind === 'gpu') label.scale.multiplyScalar(0.5)
        group.add(label)
      }
    }
    if (mode === 'network') {
      const layers = new Map<number | null, SpatialPlacement[]>()
      for (const placement of layout.placements.filter(item => item.kind === 'network' && item.node.entity_type === 'device' && item.tier !== undefined)) {
        const tier = placement.tier!
        const layer = layers.get(tier) || []
        layer.push(placement)
        layers.set(tier, layer)
      }
      const tierNodes = [...layers.values()].flat()
      const tierMinX = Math.min(...tierNodes.map(item => item.x), 0) - 2
      const tierMinZ = Math.min(...tierNodes.map(item => item.z), 0) - 2
      const tierMaxX = Math.max(...tierNodes.map(item => item.x), 0) + 2
      const tierMaxZ = Math.max(...tierNodes.map(item => item.z), 0) + 2
      for (const placements of layers.values()) {
        const role = [...new Set(placements.map(item => item.role || '未分类'))].join(' · ')
        const y = placements.reduce((sum, item) => sum + item.y, 0) / placements.length - 0.3
        const plane = new THREE.Mesh(new THREE.PlaneGeometry(tierMaxX - tierMinX, tierMaxZ - tierMinZ), new THREE.MeshBasicMaterial({ color: networkRoleColor(role), transparent: true, opacity: 0.035, side: THREE.DoubleSide, depthWrite: false }))
        plane.rotation.x = -Math.PI / 2
        plane.position.set((tierMinX + tierMaxX) / 2, y, (tierMinZ + tierMaxZ) / 2)
        decoration.add(plane)
        const label = labelSprite(container.ownerDocument, role, networkRoleColor(role))
        if (label) { label.position.set(tierMinX - 1, y + 0.5, (tierMinZ + tierMaxZ) / 2); decoration.add(label) }
      }
    }
    scene.add(decoration)
    setSpatialSelection(objects.root, selection)
    resize()
    reset()
  }
  container.appendChild(renderer.domElement)
  renderer.domElement.addEventListener('pointerdown', pointerDown, true)
  renderer.domElement.addEventListener('pointermove', pointerMove, true)
  renderer.domElement.addEventListener('pointerup', pointerUp, true)
  renderer.domElement.addEventListener('pointercancel', pointerCancel, true)
  renderer.domElement.addEventListener('lostpointercapture', pointerCancel)
  const observer = typeof view.ResizeObserver === 'function' ? new view.ResizeObserver(resize) : undefined
  observer?.observe(container)
  view.addEventListener('resize', resize)
  resize()
  function animate() {
    if (disposed) return
    controls.update()
    renderer.render(scene, camera)
    frame = view!.requestAnimationFrame(animate)
  }
  frame = view.requestAnimationFrame(animate)
  return {
    update, reset,
    select(id: string | null) { selection = id; if (objects) setSpatialSelection(objects.root, id) },
    zoom(factor: number) {
      const offset = camera.position.clone().sub(controls.target)
      offset.setLength(THREE.MathUtils.clamp(offset.length() / factor, controls.minDistance, controls.maxDistance))
      camera.position.copy(controls.target).add(offset)
      controls.update()
    },
    dispose() {
      if (disposed) return
      disposed = true
      view.cancelAnimationFrame(frame)
      observer?.disconnect()
      view.removeEventListener('resize', resize)
      renderer.domElement.removeEventListener('pointerdown', pointerDown, true)
      renderer.domElement.removeEventListener('pointermove', pointerMove, true)
      renderer.domElement.removeEventListener('pointerup', pointerUp, true)
      renderer.domElement.removeEventListener('pointercancel', pointerCancel, true)
      renderer.domElement.removeEventListener('lostpointercapture', pointerCancel)
      controls.dispose()
      disposeSpatialObjects(scene)
      renderer.dispose()
      renderer.forceContextLoss()
      renderer.domElement.remove()
    },
  }
}
