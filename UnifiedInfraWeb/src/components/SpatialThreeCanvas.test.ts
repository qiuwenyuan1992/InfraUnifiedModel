import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { expect, it } from 'vitest'
import SpatialThreeCanvas from './SpatialThreeCanvas.vue'
import type { SpatialSceneLayout } from '../services/spatialScene'

const layout: SpatialSceneLayout = { placements: [], edges: [], warnings: [], width: 20, depth: 20, height: 10 }

it('renders the space toolbar and real-data empty state without creating WebGL during SSR', async () => {
  const html = await renderToString(createSSRApp(SpatialThreeCanvas, { layout, selection: null, mode: 'space' }))
  expect(html).toContain('机柜空间 · 真实 3D')
  expect(html).toContain('重置视角')
  expect(html).toContain('当前范围没有可展示的空间节点')
  expect(html).not.toContain('<canvas')
})

it('displays network role colors and mode name independently of parent statistics', async () => {
  const html = await renderToString(createSSRApp(SpatialThreeCanvas, { layout, selection: null, mode: 'network' }))
  expect(html).toContain('网络角色分层 · 真实 3D')
  for (const role of ['T2', 'T1', 'T0', '未知角色']) expect(html).toContain(role)
  expect(html).toContain('#a78bfa')
  expect(html).not.toContain('CPU')
})
