import { ApiError } from '../api/inventory'

export const statuses: Record<string, string> = {
  queued: '排队中', running: '执行中', validating: '校验中', publishing: '发布中',
  succeeded: '已成功', failed: '已失败', canceled: '已取消', not_run: '未执行',
}
export const phaseNames: Record<string, string> = {
  data_center: '数据中心', cabinet: '机柜', device: '设备', transformer: '变压器',
  ups_group: 'UPS 组', ups: 'UPS 设备', rpp: '列头柜', pod: 'POD', interface: '网络端口',
  power_relations: '供电关系', pod_membership: 'POD 归属', spatial_relations: '空间关系',
  gpu_sync: 'GPU 同步', server_uplinks: '服务器上联', links_to: '物理连接',
}
export const isActive = (status: string) => ['queued', 'running', 'validating', 'publishing'].includes(status)
export function statusType(status: string): 'success' | 'danger' | 'warning' | 'info' | 'primary' {
  if (status === 'succeeded') return 'success'
  if (status === 'failed') return 'danger'
  if (status === 'queued') return 'warning'
  return isActive(status) ? 'primary' : 'info'
}
export function formatTime(value: string | null | undefined) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
}
export function errorText(error: unknown): string {
  if (error instanceof ApiError) {
    const messages: Record<number, string> = {
      400: '请求参数或游标无效，请重新查询。', 401: '本地后端尚未关闭同步管理鉴权。',
      403: '后端拒绝访问，请检查本地 inventory.auth.enabled 配置。',
      404: '任务不存在或已不可访问。', 409: '来源存在未完成任务或配置未就绪，请刷新后检查。',
      504: '后端请求超时，请稍后重试。',
    }
    return `${messages[error.status] || error.message}（HTTP ${error.status}，错误码 ${error.code}${error.requestId ? `，请求 ${error.requestId}` : ''}）`
  }
  return '网络连接失败，请检查后端服务与 Vite 代理配置后重试。'
}
