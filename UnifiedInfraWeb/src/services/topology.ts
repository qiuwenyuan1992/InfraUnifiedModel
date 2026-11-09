import { ApiError } from '../api/inventory'
import type { EdgeType, EntityType, Neighborhood, PropertyValue, TopologyGraph, TopologyNode } from '../api/topologyTypes'
import { errorText } from './display'

export const entityLabels: Record<EntityType, string> = {
  device: '设备', gpu: 'GPU', interface: '网络端口', cabinet: '机柜', data_center: '数据中心',
  pod: 'POD', transformer: '变压器', ups_group: 'UPS 组', ups: 'UPS 设备', rpp: '列头柜',
}
export const edgeLabels: Record<EdgeType, string> = {
  spatial_relation: '空间关系', composition_relation: '组成关系', network_relation: '网络关系', power_relation: '电力关系',
}
export const edgeColors: Record<EdgeType, string> = {
  spatial_relation: '#879ab4', composition_relation: '#3479df', network_relation: '#24a298', power_relation: '#d69a37',
}
export const relationLabels: Record<string, string> = {
  member_of: '归属', located_in: '位于', tagged_with: '关联标签', owns_interface: '拥有端口', contains_gpu: '包含 GPU',
  server_uplink: '服务器上联', gpu_uplink: 'GPU 上联', links_to: '物理互联（存储方向）', power_upstream: '上游供电', has_standby: '备用配置',
}
export function topologyDisplayName(node: TopologyNode): string {
  if (node.entity_type === 'data_center') {
    const cnName = typeof node.properties.cn_name === 'string' ? node.properties.cn_name.trim() : ''
    const code = typeof node.properties.code === 'string' ? node.properties.code.trim() : node.label
    if (cnName) return code && code !== cnName ? `${cnName}（${code}）` : cnName
  }
  return node.label || entityLabels[node.entity_type]
}
export function identityHelp(type: EntityType) {
  if (type === 'device') return '设备 SN（device_sn），区分大小写'
  if (type === 'gpu') return 'GPU SN（parts_sn），区分大小写'
  if (type === 'interface') return '端口 UUID（port_uuid）'
  if (['cabinet', 'data_center', 'pod'].includes(type)) return '对象 UUID（uuid）'
  return '电力对象正整数 inst_id，不是名称'
}
export function validIdentity(type: EntityType, value: string) {
  if (!value.trim()) return false
  if (['transformer', 'ups_group', 'ups', 'rpp'].includes(type)) {
    return /^[1-9]\d*$/.test(value) && BigInt(value) <= 9223372036854775807n
  }
  return true
}
export function mergeNeighborhood(graph: TopologyGraph, incoming: Neighborhood): TopologyGraph {
  const nodes = new Map(graph.nodes.map(node => [node.id, node]))
  const edges = new Map(graph.edges.map(edge => [edge.id, edge]))
  for (const node of [incoming.root, ...incoming.nodes]) nodes.set(node.id, node)
  for (const edge of incoming.edges) edges.set(edge.id, edge)
  if (nodes.size > 500 || edges.size > 1000) throw new Error('本次展开超过画布上限（500 节点 / 1000 关系），已完整拒绝合并；请缩小关系筛选或重新定位。')
  return { nodes: [...nodes.values()], edges: [...edges.values()] }
}
export function topologyError(cause: unknown) {
  if (cause instanceof ApiError) {
    const messages: Record<number, string> = {
      400: '定位参数或关系类型无效，请检查稳定 ID。', 404: '未找到该节点，请核对来源、实体类型和稳定 ID。',
      401: '后端已启用鉴权；当前控制台无登录，请检查本地 inventory.auth.enabled 配置。',
      403: '后端拒绝拓扑读取；当前控制台无登录，请检查读取权限及本地鉴权配置。',
      503: '拓扑服务或图库尚未就绪，请检查后端配置后重试。',
    }
    return `${messages[cause.status] || cause.message}（HTTP ${cause.status}，错误码 ${cause.code}，${cause.message}${cause.requestId ? `，请求 ${cause.requestId}` : ''}）`
  }
  if (cause instanceof Error && !(cause instanceof TypeError)) return cause.message
  return errorText(cause)
}
export function propertyText(value: PropertyValue | undefined) {
  if (value === undefined) return '—'
  if (value === null) return 'null'
  return typeof value === 'object' ? JSON.stringify(value, null, 2) : String(value)
}
export function planeLabel(value: PropertyValue | undefined) {
  const labels: Record<string, string> = { '1': '管理平面', '2': '计算平面', '3': '存储平面', '4': '带外平面' }
  return labels[String(value)] ? `${labels[String(value)]}（${value}）` : propertyText(value)
}
