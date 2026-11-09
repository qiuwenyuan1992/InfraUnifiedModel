<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import cytoscape, { type Core, type ElementDefinition } from 'cytoscape'
import type { TopologyGraph, TopologySelection } from '../api/topologyTypes'
import { edgeColors, entityLabels, planeLabel, relationLabels } from '../services/topology'

const props = defineProps<{ graph: TopologyGraph; selection: TopologySelection | null; rootId: string }>()
const emit = defineEmits<{ select: [selection: TopologySelection] }>()
const container = ref<HTMLDivElement>()
let cy: Core | undefined
let observer: ResizeObserver | undefined

function updateGraph() {
  if (!cy) return
  const graph = cy
  const ids = new Set([...props.graph.nodes.map(node => `node:${node.id}`), ...props.graph.edges.map(edge => `edge:${edge.id}`)])
  const initial = graph.nodes().empty() || (graph.nodes().length === 1 && props.graph.nodes.length > 1)
  const additions: ElementDefinition[] = []
  graph.batch(() => {
    graph.elements().filter(element => !ids.has(element.id())).remove()
    props.graph.nodes.forEach((node, index) => {
      const id = `node:${node.id}`
      const identityLabel = node.label && node.label !== node.stable_id ? `${node.label}\n${node.stable_id}` : node.stable_id
      const data = { id, label: `${entityLabels[node.entity_type]}\n${identityLabel}`, typeLabel: entityLabels[node.entity_type], root: node.id === props.rootId ? 1 : 0 }
      const existing = graph.getElementById(id)
      if (existing.nonempty()) existing.data(data)
      else {
        const relation = props.graph.edges.find(edge => edge.from === node.id || edge.to === node.id)
        const anchorId = relation ? relation.from === node.id ? relation.to : relation.from : props.rootId
        const anchor = graph.getElementById(`node:${anchorId}`)
        const center = anchor.nonempty() ? anchor.position() : { x: 0, y: 0 }
        const angle = index * 2.39996
        const radius = 110 + 30 * Math.sqrt(index)
        additions.push({ group: 'nodes', data, position: node.id === props.rootId ? { x: 0, y: 0 } : { x: center.x + Math.cos(angle) * radius, y: center.y + Math.sin(angle) * radius } })
      }
    })
    graph.add(additions)
    for (const edge of props.graph.edges) {
      const id = `edge:${edge.id}`
      const relation = edge.edge_type === 'power_relation' && edge.relation_kind === 'member_of' ? 'UPS 成员' : relationLabels[edge.relation_kind] || edge.relation_kind
      const path = edge.properties.power_path
      const plane = edge.properties.plane
      const label = [relation, path !== undefined && path !== null ? `路径 ${path}` : '', plane !== undefined && plane !== null ? planeLabel(plane) : ''].filter(Boolean).join(' · ')
      const data = { id, source: `node:${edge.from}`, target: `node:${edge.to}`, label, color: edgeColors[edge.edge_type] }
      const existing = graph.getElementById(id)
      if (existing.nonempty()) existing.data(data)
      else graph.add({ group: 'edges', data })
    }
  })
  if (initial && graph.nodes().nonempty()) fit()
  updateSelection()
}
function updateSelection() {
  if (!cy) return
  cy.elements().unselect()
  if (props.selection) cy.getElementById(`${props.selection.kind}:${props.selection.id}`).select()
}
function fit() { if (cy?.nodes().nonempty()) cy.fit(undefined, 48) }
function zoom(factor: number) {
  if (cy) cy.zoom({ level: cy.zoom() * factor, renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 } })
}
watch(() => props.graph, updateGraph)
watch(() => props.selection, updateSelection)
onMounted(() => {
  cy = cytoscape({
    container: container.value, elements: [], layout: { name: 'preset' }, minZoom: 0.15, maxZoom: 3,
    style: [
      { selector: 'node', style: { label: 'data(label)', 'background-color': '#edf4ff', 'border-color': '#3479df', 'border-width': 2, color: '#203249', width: 44, height: 44, 'font-size': 11, 'text-valign': 'bottom', 'text-margin-y': 8, 'text-wrap': 'ellipsis', 'text-max-width': '130px' } },
      { selector: 'node[root = 1]', style: { 'border-width': 4, 'background-color': '#cfe2ff' } },
      { selector: 'edge', style: { label: 'data(label)', 'curve-style': 'bezier', 'line-color': 'data(color)', 'target-arrow-color': 'data(color)', 'target-arrow-shape': 'triangle', width: 2, 'font-size': 9, color: '#677b94', 'text-background-color': '#fff', 'text-background-opacity': 0.9, 'text-background-padding': '2px', 'text-rotation': 'autorotate' } },
      { selector: ':selected', style: { 'overlay-color': '#3479df', 'overlay-opacity': 0.15, 'overlay-padding': 7 } },
    ],
  })
  cy.on('tap', 'node, edge', event => {
    const element = event.target
    emit('select', { kind: element.isNode() ? 'node' : 'edge', id: element.id().slice(5) })
  })
  observer = new ResizeObserver(() => cy?.resize())
  if (container.value) observer.observe(container.value)
  updateGraph()
})
onBeforeUnmount(() => { observer?.disconnect(); cy?.destroy(); cy = undefined })
</script>

<template>
  <div class="topology-canvas-shell">
    <div ref="container" class="topology-canvas" role="img" aria-label="资产拓扑画布，可拖拽节点、缩放并选择节点或关系" />
    <div class="canvas-controls"><el-button size="small" aria-label="放大画布" @click="zoom(1.25)">放大</el-button><el-button size="small" aria-label="缩小画布" @click="zoom(0.8)">缩小</el-button><el-button size="small" @click="fit">适应画布</el-button></div>
  </div>
</template>
