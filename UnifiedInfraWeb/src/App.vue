<script setup lang="ts">
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import { Connection, DataLine, Refresh, Plus, CircleCheck, Warning, Clock, ArrowRight } from '@element-plus/icons-vue'
import { useManagement } from './composables/useManagement'
import { formatTime, isActive, statuses, statusType } from './services/display'
import CreateRoundDialog from './components/CreateRoundDialog.vue'
import DetailDrawer from './components/DetailDrawer.vue'
import DiagnosticDrawer from './components/DiagnosticDrawer.vue'
const TopologyBrowser = defineAsyncComponent(() => import('./components/TopologyBrowser.vue'))
const SpatialBrowser = defineAsyncComponent(() => import('./components/SpatialBrowser.vue'))
const OkbConsole = defineAsyncComponent(() => import('./components/OkbConsole.vue'))
const DiagnosisConsole = defineAsyncComponent(() => import('./components/DiagnosisConsole.vue'))

const {
  items, loading, error, nextCursor, history, load, next, previous, section,
  sources, sourcesLoading, sourcesLoaded, sourcesError, sourceFilter, statusFilter, pageSize, filters,
  selectedId, selected, detailLoading, detailError, cancelLoading, createOpen, diagnostic,
  loadSources, loadDetail, openDetail, closeDetail, cancel, created, showDiagnostics, sourceName,
} = useManagement()
const okbOpen = ref(false)
watch(section, value => {
  if (value === 'topology' || value === 'spatial') { closeDetail(); diagnostic.value = null; createOpen.value = false }
  if (String(value).startsWith('okb')) okbOpen.value = true // 从子页面进入时自动展开菜单
})
const okbTitles: Record<string, { title: string; desc: string }> = {
  'okb:list': { title: '知识清单', desc: '全部知识总览：异常类型、因果规则、告警映射与复核状态，行内直达 trace/证据/治理。' },
  'okb:graph': { title: '知识图谱', desc: '力导向因果图：节点为异常类型，连线为因果边，颜色按证据等级、粗细按置信度。' },
  'okb:query': { title: '图谱查询', desc: 'lookup 按告警查候选异常；trace 遍历候选根因链，每跳带条件求值与证据等级。' },
  'okb:review': { title: '复核队列', desc: '被否决降级后待人工复核的因果边，可确认恢复或维持否决。' },
  'okb:ingest': { title: '资料摄取', desc: '脱敏工单/厂商文档/专家经验入库，经提取生成候选知识；独立工单印证自动升 curated。' },
  'okb:report': { title: '诊断反馈', desc: '上报诊断使用的边与最终判定，确认升置信度、否决降级入复核，幂等去重。' },
}
// 一级页：故障诊断（与故障知识平级）
const sectionTitles: Record<string, { title: string; desc: string }> = {
  'diag:run': { title: '故障诊断', desc: 'LangGraph 判定序流式执行：告警归一 → 传播推演 → 跨域取证提示 → 结论 → 飞轮回写；输入侧当前为模拟告警。' },
}
const okbSection = computed(() => (String(section.value).startsWith('okb') ? String(section.value) : ''))
const sectionTitle = computed(() => {
  if (okbSection.value) return okbTitles[okbSection.value]?.title ?? '故障知识'
  if (sectionTitles[section.value]) return sectionTitles[section.value].title
  return ({ rounds: '同步任务', sources: '数据来源', topology: '业务关联拓扑', spatial: '空间三维' })[section.value as 'rounds' | 'sources' | 'topology' | 'spatial']
})
const sectionDescription = computed(() => {
  if (okbSection.value) return okbTitles[okbSection.value]?.desc ?? ''
  if (sectionTitles[section.value]) return sectionTitles[section.value].desc
  return ({ rounds: '以完整同步轮次为单位，跟踪采集进度、阶段结果与诊断。', sources: '读取后端真实来源清单；一期不提供来源增删改。', topology: '从数据中心或 POD 浏览设备，自动展示真实供电、GPU 与网络关联。', spatial: '以示意三维空间查看机柜与设备，按 CMDB role 查看网络层级。' })[section.value as 'rounds' | 'sources' | 'topology' | 'spatial']
})
const summary = computed(() => [
  { label: '本页任务', value: items.value.length, note: '当前筛选 · 非全量统计', icon: DataLine, tone: 'blue' },
  { label: '本页活动任务', value: items.value.filter(item => isActive(item.status)).length, note: '排队 / 执行 / 校验 / 发布', icon: Clock, tone: 'amber' },
  { label: '本页成功任务', value: items.value.filter(item => item.status === 'succeeded').length, note: '后端已确认完成', icon: CircleCheck, tone: 'green' },
  { label: '本页失败任务', value: items.value.filter(item => item.status === 'failed').length, note: '查看阶段与脱敏诊断', icon: Warning, tone: 'red' },
])
</script>

<template>
  <el-config-provider :locale="zhCn">
  <div class="app-shell">
    <aside class="sidebar">
      <div class="brand"><div class="brand-mark"><el-icon :size="25"><Connection /></el-icon></div><div><strong>统一基础设施</strong><span>INFRA CONSOLE</span></div></div>
      <div class="nav-label">控制中心</div>
      <nav aria-label="主导航">
        <button :class="{ active: section === 'rounds' }" @click="section = 'rounds'"><el-icon><DataLine /></el-icon>同步任务<span class="nav-indicator" /></button>
        <button :class="{ active: section === 'sources' }" @click="section = 'sources'"><el-icon><Connection /></el-icon>数据来源</button>
        <button :class="{ active: section === 'topology' }" @click="section = 'topology'"><el-icon><Connection /></el-icon>拓扑浏览</button>
        <button :class="{ active: section === 'spatial' }" @click="section = 'spatial'"><el-icon><DataLine /></el-icon>空间三维</button>
        <button :class="{ active: section === 'diag:run' }" @click="section = 'diag:run'"><el-icon><Warning /></el-icon>故障诊断</button>
        <button class="nav-group" :class="{ open: okbOpen }" @click="okbOpen = !okbOpen">
          <el-icon><Warning /></el-icon>故障知识<el-icon class="nav-arrow" :class="{ open: okbOpen }"><ArrowRight /></el-icon>
        </button>
        <div v-show="okbOpen" class="nav-submenu">
          <div class="nav-submenu-label">知识管理</div>
          <button :class="{ active: section === 'okb:list' }" @click="section = 'okb:list'">知识清单</button>
          <button :class="{ active: section === 'okb:graph' }" @click="section = 'okb:graph'">知识图谱</button>
          <button :class="{ active: section === 'okb:query' }" @click="section = 'okb:query'">图谱查询</button>
          <div class="nav-submenu-label">队列管理</div>
          <button :class="{ active: section === 'okb:review' }" @click="section = 'okb:review'">复核队列</button>
          <div class="nav-submenu-label">提取数据</div>
          <button :class="{ active: section === 'okb:ingest' }" @click="section = 'okb:ingest'">资料摄取</button>
          <button :class="{ active: section === 'okb:report' }" @click="section = 'okb:report'">诊断反馈</button>
        </div>
      </nav>
      <div class="sidebar-foot"><span class="local-dot" />本地开发控制台<p>一期 · 来源同步管理<br />仅手动创建，无自动调度</p></div>
    </aside>
    <div class="main-shell">
      <header class="topbar"><div><span class="muted">控制中心</span><span class="breadcrumb-separator">/</span><strong>{{ sectionTitle }}</strong></div><el-tag effect="plain" type="info">本地环境 · 无登录</el-tag></header>
      <main>
        <div class="page-heading"><div><div class="eyebrow">INVENTORY MANAGEMENT</div><h1>{{ sectionTitle }}</h1><p>{{ sectionDescription }}</p></div><el-button v-if="section === 'rounds'" type="primary" :icon="Plus" :disabled="sourcesLoading || !sourcesLoaded || !!sourcesError || !sources.some(source => source.enabled)" @click="createOpen = true">创建同步任务</el-button></div>
        <el-alert v-if="sourcesError" :title="sourcesError" type="error" :closable="false" show-icon class="spaced"><template #default><el-button link type="primary" @click="loadSources">重新读取来源</el-button></template></el-alert>
        <template v-if="section === 'rounds'">
          <section class="summary-grid" aria-label="本页任务统计">
            <article v-for="card in summary" :key="card.label" class="summary-card"><div><span class="summary-label">{{ card.label }}</span><div class="summary-value">{{ loading || error ? '—' : card.value }}</div><span class="summary-note">{{ card.note }}</span></div><div class="summary-icon" :class="card.tone"><el-icon :size="24"><component :is="card.icon" /></el-icon></div></article>
          </section>
          <section class="panel">
            <div class="panel-title"><div><h2>任务记录</h2><span class="muted">每 10 秒刷新当前页 · 仅选中活动任务刷新详情</span></div><el-button :icon="Refresh" :loading="loading" @click="load(filters)">刷新</el-button></div>
            <div class="filters">
              <el-select v-model="sourceFilter" clearable filterable placeholder="全部来源" aria-label="来源筛选" :loading="sourcesLoading"><el-option v-for="source in sources" :key="source.id" :label="source.name" :value="source.id" /></el-select>
              <el-select v-model="statusFilter" clearable placeholder="全部状态" aria-label="任务状态筛选"><el-option v-for="(label, status) in statuses" v-show="status !== 'not_run'" :key="status" :label="label" :value="status" /></el-select>
              <el-select v-model="pageSize" aria-label="每页条数"><el-option v-for="size in [20, 50, 100, 200]" :key="size" :label="`每页 ${size} 条`" :value="size" /></el-select>
              <span class="filter-hint">变更筛选后从第一页查询</span>
            </div>
            <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="spaced" />
            <el-table v-loading="loading" :data="items" row-key="id" class="task-table">
              <el-table-column label="任务 / 来源" min-width="250"><template #default="{ row }"><el-button link type="primary" class="mono" @click="openDetail(row.id)">{{ row.id }}</el-button><div class="table-secondary">{{ sourceName(row.source_id) }}</div></template></el-table-column>
              <el-table-column label="状态" width="120"><template #default="{ row }"><el-tag :type="statusType(row.status)" effect="light">{{ statuses[row.status] || row.status }}</el-tag><div v-if="row.cancel_requested_at" class="table-secondary">已请求取消</div></template></el-table-column>
              <el-table-column label="阶段进度" min-width="140"><template #default="{ row }"><div class="phase-progress"><span>{{ row.completed_phases }} / {{ row.total_phases }}</span><el-progress :percentage="row.total_phases ? Math.round(row.completed_phases / row.total_phases * 100) : 0" :show-text="false" :stroke-width="5" /></div></template></el-table-column>
              <el-table-column label="创建时间" min-width="160"><template #default="{ row }">{{ formatTime(row.created_at) }}</template></el-table-column>
              <el-table-column label="错误码" min-width="125"><template #default="{ row }"><span :class="{ 'error-code': row.error_code }">{{ row.error_code || '—' }}</span></template></el-table-column>
              <el-table-column label="操作" width="158" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openDetail(row.id)">详情</el-button><el-button link type="primary" @click="showDiagnostics(row.id, true, row.source_id)">诊断</el-button><el-button v-if="isActive(row.status)" link type="danger" :disabled="cancelLoading || !!row.cancel_requested_at" @click="cancel(row)">取消</el-button></template></el-table-column>
              <template #empty><el-empty :description="error ? '任务加载失败，请检查连接后重试' : loading ? '正在读取同步任务' : '当前条件下暂无同步任务'" :image-size="96"><el-button v-if="!error && !loading && sources.some(source => source.enabled)" type="primary" plain @click="createOpen = true">创建首个任务</el-button></el-empty></template>
            </el-table>
            <div class="pagination"><span class="muted">第 {{ history.length + 1 }} 页 · 本页 {{ items.length }} 条 · 游标分页</span><el-button :disabled="!history.length || loading" @click="previous(filters)">上一页</el-button><el-button :disabled="!nextCursor || loading" @click="next(filters)">下一页</el-button></div>
          </section>
          <p class="page-footnote">完整同步执行来源配置内的全部阶段，阶段总数以任务 API 返回值为准；成功阶段数不等于对象采集比例，失败或取消不会回滚已写入图数据。</p>
        </template>
        <section v-else-if="section === 'sources'" class="panel">
          <div class="panel-title"><div><h2>来源清单</h2><span class="muted">{{ sourcesLoaded && !sourcesError ? `已读取 ${sources.length} 个真实来源` : '从后端分页读取完整来源清单' }}</span></div><el-button :icon="Refresh" :loading="sourcesLoading" @click="loadSources">刷新来源</el-button></div>
          <el-table v-loading="sourcesLoading" :data="sources" row-key="id"><el-table-column prop="name" label="来源名称" min-width="160" /><el-table-column prop="id" label="来源 ID" min-width="250" /><el-table-column prop="adapter_kind" label="适配器" min-width="120" /><el-table-column label="状态" width="120"><template #default="{ row }"><el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '已启用' : '已停用' }}</el-tag></template></el-table-column><template #empty><el-empty :description="sourcesError ? '来源加载失败，请重试' : sourcesLoading ? '正在读取来源清单' : '后端尚未配置来源'" :image-size="96" /></template></el-table>
        </section>
        <TopologyBrowser v-else-if="section === 'topology'" :sources="sources" :sources-loading="sourcesLoading" :sources-loaded="sourcesLoaded" :sources-error="sourcesError" />
        <SpatialBrowser v-else-if="section === 'spatial'" :sources="sources" :sources-loading="sourcesLoading" :sources-loaded="sourcesLoaded" :sources-error="sourcesError" />
        <OkbConsole v-else-if="okbSection" :page="okbSection" @navigate="(p: string) => section = p" />
        <DiagnosisConsole v-else-if="section === 'diag:run'" />
      </main>
    </div>
    <CreateRoundDialog v-model="createOpen" :sources="sources" @created="created" />
    <DetailDrawer :id="selectedId" :round="selected" :loading="detailLoading" :error="detailError" :cancel-loading="cancelLoading" :source-name="selected ? sourceName(selected.source_id) : ''" @close="closeDetail" @refresh="loadDetail" @cancel="cancel" @diagnostics="showDiagnostics" />
    <DiagnosticDrawer v-if="diagnostic" :target="diagnostic" :sources="sources" @close="diagnostic = null" />
  </div>
  </el-config-provider>
</template>
