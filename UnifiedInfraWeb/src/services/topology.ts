import { ApiError } from '../api/inventory'
import type { EdgeType, EntityType, Neighborhood, PropertyValue, TopologyGraph, TopologyNode } from '../api/topologyTypes'
import { errorText } from './display'

export const entityLabels: Record<EntityType, string> = {
  device: '设备', gpu: 'GPU', interface: '网络端口', cabinet: '机柜', room: '房间', data_center: '数据中心',
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
  if (node.entity_type === 'pod') {
    // POD 的 full_name 已含数据中心前缀（如「中卫算力网络B1-POD000」），
    // 不同站点的 POD 同名（都叫 POD000），靠 full_name 区分；缺失时回退 label。
    const fullName = typeof node.properties.full_name === 'string' ? node.properties.full_name.trim() : ''
    if (fullName) return fullName
  }
  return node.label || entityLabels[node.entity_type]
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
// 目录卡片展示来源计数快照；缺失时用 — 占位，不猜测为 0，也不展示稳定 ID。
// 机柜卡片显示设备总数（下钻目标），数据中心/房间显示机柜数（下钻目标）。
export function directoryCountSummary(node: TopologyNode) {
  const entries: [string, PropertyValue | undefined][] = node.entity_type === 'cabinet'
    ? [
        ['设备', node.properties.device_num],
        ['服务器', node.properties.server_num],
        ['网络设备', node.properties.network_device_num],
      ]
    : [
        ['机柜', node.properties.cabinet_num],
        ['服务器', node.properties.server_num],
        ['网络设备', node.properties.network_device_num],
      ]
  return entries.map(([label, value]) => `${label} ${value == null ? '—' : String(value)}`).join(' · ')
}

// POD 卡片摘要：基础编码与平面（均为 CMDB 返回字段），不展示 UUID；
// 数据中心信息由卡片标题的 full_name 携带。
export function podCardSummary(node: TopologyNode) {
  const parts: string[] = []
  const code = typeof node.properties.basic_code === 'string' ? node.properties.basic_code.trim() : ''
  if (code) parts.push(code)
  const plane = planeLabel(node.properties.plane)
  if (plane !== '—') parts.push(plane)
  return parts.length ? parts.join(' · ') : '—'
}

// 设备子类型字典：与 docs/cmdb/dict_device_type.md 一一对应；parent_type_id 54 为网络设备。
const deviceTypeNames: Record<number, string> = {
  52: '服务器', 53: '存储设备', 54: '网络设备', 55: '其他设备',
  152: '整机柜服务器', 187: '智能网卡服务器', 58: 'GPU服务器', 138: '高密服务器', 57: '通用服务器', 202: '智能网卡云主机服务器',
  63: '存储交换机', 65: '磁带库', 62: '存储控制器', 66: '磁带机', 64: '磁盘存储',
  205: '串口服务器', 189: '框式路由器', 74: '堡垒机', 75: '防火墙', 71: '分光器', 176: '分流器', 72: '负载均衡', 68: 'VPN',
  153: '网络测试仪', 190: '盒式交换机', 204: '盒式路由器', 76: '波分设备', 188: '框式交换机', 214: '光网络测试仪', 216: '总线设备',
  178: '漏洞扫描系统', 183: '安全扫描设备', 79: '视频设备', 166: '网站监测系统', 174: '其他安全设备', 179: '入侵监测系统',
  200: '数据库一体机', 160: '漏洞管理系统', 208: '语音交换机', 158: '时钟服务器', 86: '控制器', 182: '配置核查系统',
  185: 'WEB服务器', 85: '探针设备', 168: '打包服务器', 173: '网管服务器', 80: '显示器', 157: '资质服务器', 159: '入侵检测系统',
  184: '云服务器密码机', 206: 'Power Shelf', 83: '机柜', 84: 'UPS', 121: '未知', 82: '加密设备', 154: '安全服务器',
  175: '会话边界控制器', 164: '运维安全审计系统', 162: 'WEB应用防护系统', 163: '抗DDOS系统', 161: '配置管理系统',
  177: '网络审计系统', 207: '网关', 69: '其他网络设备', 186: 'DB服务器', 59: '视频服务器', 201: '态势感知', 81: '密码机',
  61: '小型机', 209: '数据库审计', 210: '服务器密码机', 211: '分水器', 212: '快接头插头（公头）', 213: '快接头插头（母头）',
  215: '云手机服务器', 218: '其他存储设备', 301: '消磁设备', 302: 'SMN管理节点', 303: 'NPU计算刀片',
}

// 设备卡片摘要：类型（按字典解析）· IP（最多展示2个）· 角色（仅网络设备）。
export function deviceCardSummary(node: TopologyNode) {
  const parts: string[] = []
  const typeID = Number(node.properties.device_type_id)
  if (Number.isInteger(typeID) && typeID > 0) parts.push(deviceTypeNames[typeID] ?? `类型 ${typeID}`)
  const ips = Array.isArray(node.properties.all_ips) ? node.properties.all_ips.filter((value): value is string => typeof value === 'string' && value.trim() !== '') : []
  if (ips.length) parts.push(ips.length > 2 ? `IP ${ips.slice(0, 2).join(', ')} 等${ips.length}个` : `IP ${ips.join(', ')}`)
  // 角色仅对网络设备有确认语义（T0/T1）；非网络设备的角色值不展示。
  if (Number(node.properties.parent_type_id) === 54 && typeof node.properties.role === 'string' && node.properties.role.trim()) parts.push(node.properties.role.trim())
  return parts.length ? parts.join(' · ') : '—'
}
