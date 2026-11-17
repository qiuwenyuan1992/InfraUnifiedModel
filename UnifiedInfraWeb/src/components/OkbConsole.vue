<template>
  <div class="okb-console">
    <el-alert v-if="connectionError" type="error" :closable="false" show-icon class="spaced"
      title="OKB 服务不可达"
      :description="`${connectionError}——请确认 OKB 后端已启动（默认 :18090）且 Vite 代理已配置 /api → OKB。`" />

    <!-- 知识清单 -->
    <template v-if="page === 'okb:list'">
      <section class="panel">
        <div class="panel-title">
          <div><h2>知识总览</h2><span class="muted">按场景或类型浏览库内全部知识</span></div>
          <div class="okb-toolbar">
            <el-radio-group v-model="listView" size="small">
              <el-radio-button value="scenarios">场景卡片</el-radio-button>
              <el-radio-button value="groups">分类列表</el-radio-button>
            </el-radio-group>
            <template v-if="listView === 'groups'">
              <el-input v-model="listFilter.q" placeholder="搜索编码 / 名称 / signature" clearable class="okb-search" @keyup.enter="loadAllLists" />
              <el-select v-model="listFilter.status" placeholder="全部状态" clearable class="okb-select">
                <el-option label="curated" value="curated" /><el-option label="draft" value="draft" />
              </el-select>
              <el-button type="primary" :icon="Refresh" :loading="loading.list" @click="loadAllLists">刷新</el-button>
              <el-tag v-if="listFiltered" type="warning" size="small" closable @close="clearListFilter">
                筛选中：{{ listFilter.q || listFilter.status }}
              </el-tag>
            </template>
          </div>
        </div>

        <!-- 场景卡片墙 -->
        <div v-if="listView === 'scenarios'">
          <el-empty v-if="scenarioCards && scenarioCards.length === 0"
            description="尚无场景；场景是领域内有主题的知识分组，可经 POST /api/v1/scenarios 定义" :image-size="80" />
          <div v-else class="okb-cards">
            <div v-for="card in scenarioCards ?? []" :key="card.code" class="okb-card"
              :style="{ '--domain-color': DOMAIN_COLORS[card.domain] ?? DOMAIN_COLORS.other }"
              @click="doScenarioDetail(card.code)">
              <div class="okb-card-head">
                <i class="okb-card-dot" />
                <span class="okb-card-name">{{ card.name }}</span>
                <span class="muted">{{ domainLabel(card.domain) }}</span>
              </div>
              <div class="okb-card-desc">{{ card.description || card.code }}</div>
              <div class="okb-card-stats">
                <span><b>{{ card.node_count }}</b> 节点</span>
                <span><b>{{ card.edge_count }}</b> 边<template v-if="card.draft_edges > 0">（含 {{ card.draft_edges }} 草稿）</template></span>
                <span><b>{{ card.alert_count }}</b> 告警入口</span>
                <span v-if="card.mitigation_count > 0"><b>{{ card.mitigation_count }}</b> 处置</span>
              </div>
              <div class="okb-card-health">
                <el-progress :percentage="edgeHealth(card)" :stroke-width="6"
                  :color="edgeHealth(card) === 100 ? '#38a078' : '#d79b35'" />
                <span class="muted">{{ card.curated_edges }}/{{ card.edge_count }} 边已核</span>
              </div>
            </div>
          </div>
        </div>

        <!-- 分类折叠列表（原视图） -->
        <template v-else>
        <el-collapse v-model="listOpen" class="okb-collapse">
          <el-collapse-item name="anomaly">
            <template #title>
              <span class="okb-section-name"><el-icon><Warning /></el-icon>异常类型（节点）</span>
              <span class="muted">{{ listAnomaly?.total ?? '—' }} 条 · {{ anomalyByDomain.length }} 个领域</span>
            </template>
            <div v-for="group in anomalyByDomain" :key="group.domain" class="okb-domain-group">
              <div class="okb-domain-head">
                <i :style="{ background: DOMAIN_COLORS[group.domain] ?? DOMAIN_COLORS.other }" />
                <span>{{ domainLabel(group.domain) }}</span>
                <span class="muted">{{ group.rows.length }} 条</span>
              </div>
              <el-table :data="group.rows" size="small" class="task-table" row-key="id">
                <el-table-column prop="stable_code" label="异常编码" min-width="200" show-overflow-tooltip />
                <el-table-column prop="name" label="名称" min-width="140" />
                <el-table-column label="状态" width="90">
                  <template #default="{ row }"><el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag></template>
                </el-table-column>
                <el-table-column label="更新时间" min-width="150">
                  <template #default="{ row }">{{ formatTime(row.updated_at) }}</template>
                </el-table-column>
                <el-table-column label="操作" width="130" fixed="right">
                  <template #default="{ row }">
                    <el-button link type="primary" size="small" @click="goto('okb:query', row.stable_code, true)">trace</el-button>
                    <el-button link size="small" @click="doEvidence(row.stable_code)">证据</el-button>
                  </template>
                </el-table-column>
              </el-table>
            </div>
            <el-empty v-if="listAnomaly && anomalyByDomain.length === 0" description="暂无异常类型" :image-size="60" />
          </el-collapse-item>
          <el-collapse-item name="rule">
            <template #title>
              <span class="okb-section-name"><el-icon><Link /></el-icon>因果规则（边）</span>
              <span class="muted">{{ listRule?.total ?? '—' }} 条</span>
            </template>
            <el-table :data="listRule?.rows ?? []" size="small" class="task-table" row-key="id">
              <el-table-column label="因果（源 → 目标）" min-width="260">
                <template #default="{ row }"><span class="mono">{{ row.source_code }}</span><el-icon class="okb-arrow"><ArrowRight /></el-icon><span class="mono">{{ row.target_code }}</span></template>
              </el-table-column>
              <el-table-column label="类型" width="95">
                <template #default="{ row }">
                  <el-tag :type="row.edge_type === 'mitigate' ? '' : row.edge_type === 'indicate' ? 'info' : 'primary'" size="small">{{ edgeTypeLabel(row.edge_type) }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="证据等级" width="120">
                <template #default="{ row }"><el-tag :type="levelTag(row.evidence_level)" size="small">{{ levelLabel(row.evidence_level) }}</el-tag></template>
              </el-table-column>
              <el-table-column label="置信度" width="85">
                <template #default="{ row }">{{ row.confidence.toFixed(2) }}</template>
              </el-table-column>
              <el-table-column label="印证" width="80">
                <template #default="{ row }">{{ row.case_support_count }} 工单</template>
              </el-table-column>
              <el-table-column label="状态" width="100">
                <template #default="{ row }">
                  <el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag>
                  <el-tag v-if="row.review_flagged" type="danger" size="small">待复核</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="180" fixed="right">
                <template #default="{ row }">
                  <el-button link size="small" @click="doEvidence(row.id)">证据</el-button>
                  <el-button link type="success" size="small" @click="doConfirmEdge(row.id)">确认</el-button>
                  <el-button link type="danger" size="small" @click="doRejectEdge(row.id)">否决</el-button>
                  <el-button link size="small" @click="report.edgeId = row.id; goto('okb:report')">反馈</el-button>
                </template>
              </el-table-column>
              <template #empty><el-empty description="暂无因果规则" :image-size="60" /></template>
            </el-table>
          </el-collapse-item>
          <el-collapse-item name="mapping">
            <template #title>
              <span class="okb-section-name"><el-icon><Bell /></el-icon>告警映射（入口）</span>
              <span class="muted">{{ listMapping?.total ?? '—' }} 条</span>
            </template>
            <el-table :data="listMapping?.rows ?? []" size="small" class="task-table" row-key="id">
              <el-table-column prop="alert_signature" label="告警 signature" min-width="200" show-overflow-tooltip />
              <el-table-column label="候选异常" min-width="180">
                <template #default="{ row }"><span class="mono">{{ row.stable_code }}</span><div class="table-secondary">{{ row.name }}</div></template>
              </el-table-column>
              <el-table-column label="置信度" width="85">
                <template #default="{ row }">{{ row.confidence.toFixed(2) }}</template>
              </el-table-column>
              <el-table-column label="证据等级" width="120">
                <template #default="{ row }"><el-tag :type="levelTag(row.evidence_level)" size="small">{{ levelLabel(row.evidence_level) }}</el-tag></template>
              </el-table-column>
              <el-table-column label="状态" width="90">
                <template #default="{ row }"><el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag></template>
              </el-table-column>
              <el-table-column label="操作" width="120" fixed="right">
                <template #default="{ row }">
                  <el-button link type="primary" size="small" @click="alertSignature = row.alert_signature; goto('okb:query'); doLookup()">lookup</el-button>
                </template>
              </el-table-column>
              <template #empty><el-empty description="暂无告警映射" :image-size="60" /></template>
            </el-table>
          </el-collapse-item>
          <el-collapse-item name="review">
            <template #title>
              <span class="okb-section-name"><el-icon><CircleCheck /></el-icon>复核队列（被否决待处理）</span>
              <span class="muted">{{ reviewEdges?.length ?? '—' }} 条</span>
            </template>
            <el-table :data="reviewEdges ?? []" size="small" class="task-table" row-key="id">
              <el-table-column label="因果（源 → 目标）" min-width="260">
                <template #default="{ row }"><span class="mono">{{ row.source_code }}</span><el-icon class="okb-arrow"><ArrowRight /></el-icon><span class="mono">{{ row.target_code }}</span></template>
              </el-table-column>
              <el-table-column label="置信度" width="90">
                <template #default="{ row }">{{ row.confidence.toFixed(2) }}</template>
              </el-table-column>
              <el-table-column label="状态" width="90">
                <template #default="{ row }"><el-tag type="info" size="small">{{ row.status }}</el-tag></template>
              </el-table-column>
              <el-table-column label="操作" width="170" fixed="right">
                <template #default="{ row }">
                  <el-button link type="success" size="small" @click="doConfirmEdge(row.id)">确认恢复</el-button>
                  <el-button link type="danger" size="small" @click="doRejectEdge(row.id)">维持否决</el-button>
                </template>
              </el-table-column>
              <template #empty><el-empty description="复核队列为空" :image-size="60" /></template>
            </el-table>
          </el-collapse-item>
        </el-collapse>
        </template>
      </section>

      <!-- 场景详情对话框 -->
      <el-dialog v-model="scenarioVisible" :title="scenarioDetail?.scenario?.name" width="860px">
        <div v-if="scenarioDetail">
          <div class="okb-scenario-head">
            <el-tag size="small">{{ domainLabel(scenarioDetail.scenario.domain) }}</el-tag>
            <span class="muted">{{ scenarioDetail.scenario.description || scenarioDetail.scenario.code }}</span>
          </div>
          <div class="okb-section-name" style="margin:12px 0 6px">异常节点（{{ scenarioDetail.nodes.length }}）</div>
          <el-table :data="scenarioDetail.nodes" size="small" class="task-table" row-key="id">
            <el-table-column prop="stable_code" label="异常编码" min-width="220" show-overflow-tooltip />
            <el-table-column prop="name" label="名称" min-width="140" />
            <el-table-column label="状态" width="90">
              <template #default="{ row }"><el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag></template>
            </el-table-column>
            <el-table-column label="操作" width="130" fixed="right">
              <template #default="{ row }">
                <el-button link type="primary" size="small" @click="goto('okb:query', row.stable_code, true)">trace</el-button>
                <el-button link size="small" @click="doEvidence(row.stable_code)">证据</el-button>
              </template>
            </el-table-column>
          </el-table>
          <div class="okb-section-name" style="margin:14px 0 6px">场景内因果边（{{ scenarioDetail.edges.length }}）</div>
          <el-table :data="scenarioDetail.edges" size="small" class="task-table" row-key="id">
            <el-table-column label="因果（源 → 目标）" min-width="260">
              <template #default="{ row }"><span class="mono">{{ row.source_code }}</span><el-icon class="okb-arrow"><ArrowRight /></el-icon><span class="mono">{{ row.target_code }}</span></template>
            </el-table-column>
            <el-table-column label="类型" width="95">
              <template #default="{ row }"><el-tag :type="row.edge_type === 'mitigate' ? '' : row.edge_type === 'indicate' ? 'info' : 'primary'" size="small">{{ edgeTypeLabel(row.edge_type) }}</el-tag></template>
            </el-table-column>
            <el-table-column label="证据等级" width="110">
              <template #default="{ row }"><el-tag :type="levelTag(row.evidence_level)" size="small">{{ levelLabel(row.evidence_level) }}</el-tag></template>
            </el-table-column>
            <el-table-column label="状态" width="90">
              <template #default="{ row }"><el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag></template>
            </el-table-column>
          </el-table>
        </div>
      </el-dialog>
    </template>

    <!-- 知识图谱 -->
    <section v-else-if="page === 'okb:graph'" class="panel">
      <div class="panel-title">
        <div><h2>故障因果图</h2><span class="muted">节点色 = 领域 · 连线 = 因果边（实线因果传播 / 青虚线处置 / 灰点线症状，粗细按置信度）</span></div>
        <div class="okb-toolbar">
          <el-select v-model="graphDomainFilter" placeholder="全部领域" clearable class="okb-select">
            <el-option v-for="d in graphDomainOptions" :key="d" :label="domainLabel(d)" :value="d" />
          </el-select>
          <el-checkbox v-model="graphIncludeDraft">显示草稿边</el-checkbox>
          <el-button type="primary" :icon="Refresh" :loading="loading.graph" @click="doGraph">加载图谱</el-button>
        </div>
      </div>
      <div v-show="graphLoaded" ref="graphContainer" class="okb-graph-area" />
      <el-empty v-if="graphEmpty" description="图中没有节点，请先在「提取数据 → 资料摄取」入库工单" :image-size="96" />
      <div v-if="graphLegend.length" class="topology-legend">
        <span v-for="item in graphLegend" :key="item.label"><i :style="{ background: item.color }" />{{ item.label }}</span>
      </div>
      <el-alert v-if="graphDraftHiddenCount > 0" type="info" :closable="false" class="spaced"
        :title="`有 ${graphDraftHiddenCount} 个节点当前仅通过未审核（草稿）边关联，图中以虚线空心圈标注`"
        description="这些知识已入库但尚未通过印证或人工审核，不参与正式 trace。勾选「显示草稿边」可查看其关联。" />
      <el-dialog v-model="nodeDialogVisible" :title="nodeDialogNode?.name" width="560px">
        <p class="dialog-intro">{{ nodeDialogNode?.stable_code }}（{{ nodeDialogNode?.status }}）</p>
        <div class="drawer-actions">
          <el-button type="primary" size="small" @click="goto('okb:query', nodeDialogNode!.stable_code, true)">从该异常 trace</el-button>
          <el-button size="small" @click="doEvidence(nodeDialogNode!.stable_code)">查看证据</el-button>
          <el-button size="small" :loading="loading.mitigations" @click="doMitigations(nodeDialogNode!.stable_code)">处置建议</el-button>
        </div>
        <div v-if="mitigations.length" class="okb-mitigations">
          <div class="muted okb-mitigations-title">缓解该异常的处置手段（mitigate 边）</div>
          <div v-for="m in mitigations" :key="m.edge_id" class="okb-mitigation-row">
            <span class="okb-hop-name">{{ m.mitigation_name }}</span>
            <el-tag :type="levelTag(m.evidence_level)" size="small">{{ levelLabel(m.evidence_level) }}</el-tag>
            <span class="muted">置信度 {{ m.confidence.toFixed(2) }} · 印证 {{ m.case_support_count }} 工单</span>
          </div>
        </div>
      </el-dialog>
    </section>

    <!-- 图谱查询 -->
    <template v-else-if="page === 'okb:query'">
      <section class="panel">
        <div class="panel-title">
          <div><h2>告警入口 lookup</h2><span class="muted">按告警 signature 检索候选异常</span></div>
        </div>
        <div class="okb-toolbar okb-toolbar-row">
          <el-input v-model="alertSignature" placeholder="告警 signature，如 ALERT_GPU_ECC" clearable class="okb-search-lg" @keyup.enter="doLookup">
            <template #prepend>signature</template>
          </el-input>
          <el-checkbox v-model="traceIncludeDraft">含草稿</el-checkbox>
          <el-button type="primary" :loading="loading.lookup" @click="doLookup">查询候选异常</el-button>
        </div>
        <el-table v-if="lookup" :data="lookup.candidates" size="small" class="task-table" row-key="anomaly_id">
          <el-table-column prop="stable_code" label="异常编码" min-width="200" show-overflow-tooltip />
          <el-table-column prop="name" label="名称" min-width="140" />
          <el-table-column label="置信度" width="90">
            <template #default="{ row }">{{ row.confidence.toFixed(2) }}</template>
          </el-table-column>
          <el-table-column label="证据等级" width="120">
            <template #default="{ row }"><el-tag :type="levelTag(row.evidence_level)" size="small">{{ levelLabel(row.evidence_level) }}</el-tag></template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }"><el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag></template>
          </el-table-column>
          <el-table-column label="操作" width="130" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="traceCode = row.stable_code; doTrace()">trace</el-button>
              <el-button link size="small" @click="doEvidence(row.stable_code)">证据</el-button>
            </template>
          </el-table-column>
          <template #empty><el-empty description="该告警暂无候选异常（可能均为草稿，勾选含草稿后重试）" :image-size="60" /></template>
        </el-table>
      </section>
      <section class="panel spaced">
        <div class="panel-title">
          <div><h2>因果链遍历 trace</h2><span class="muted">多跳候选根因链 · 每跳带条件求值与证据等级</span></div>
        </div>
        <div class="okb-toolbar okb-toolbar-row">
          <el-input v-model="traceCode" placeholder="异常 stable_code，如 gpu.ecc.error" clearable class="okb-search-lg" @keyup.enter="doTrace">
            <template #prepend>anomaly</template>
          </el-input>
          <el-input-number v-model="traceHops" :min="1" :max="5" />
          <el-button type="primary" :loading="loading.trace" @click="doTrace">遍历候选链</el-button>
        </div>
        <el-empty v-if="trace && trace.chains.length === 0" description="无候选链（可能均为草稿，勾选含草稿后重试）" :image-size="80" />
        <div v-for="(chain, i) in trace?.chains ?? []" :key="i" class="okb-chain">
          <div class="okb-chain-head">
            <el-tag :type="chain.fully_curated ? 'success' : 'warning'" size="small">
              {{ chain.fully_curated ? '逐跳已核' : '含未审核跳' }}
            </el-tag>
            <span class="muted">{{ chain.hops.length }} 跳 · 最小置信度 {{ chain.min_confidence.toFixed(2) }}</span>
          </div>
          <div class="okb-chain-hops">
            <template v-for="(hop, j) in chain.hops" :key="hop.edge_id">
              <span class="okb-hop" :class="hopClass(hop)">
                <span class="okb-hop-name">{{ hop.target_name }}</span>
                <span class="okb-hop-eval">{{ evalSummary(hop) }}</span>
              </span>
              <el-icon v-if="j < chain.hops.length - 1" class="okb-arrow"><ArrowRight /></el-icon>
            </template>
            <el-button link type="primary" size="small" @click="doEvidence(chain.hops[chain.hops.length - 1].edge_id)">边证据</el-button>
          </div>
          <div v-if="chain.combined_note" class="okb-chain-note">{{ chain.combined_note }}</div>
        </div>
      </section>
    </template>

    <!-- 复核队列 -->
    <section v-else-if="page === 'okb:review'" class="panel">
      <div class="panel-title">
        <div><h2>复核队列</h2><span class="muted">被否决降级后待人工复核的因果边</span></div>
        <el-button type="primary" :icon="Refresh" :loading="loading.review" @click="doReviewQueue">刷新队列</el-button>
      </div>
      <el-table :data="reviewEdges ?? []" size="small" class="task-table" row-key="id">
        <el-table-column label="因果（源 → 目标）" min-width="260">
          <template #default="{ row }"><span class="mono">{{ row.source_code }}</span><el-icon class="okb-arrow"><ArrowRight /></el-icon><span class="mono">{{ row.target_code }}</span></template>
        </el-table-column>
        <el-table-column label="置信度" width="90">
          <template #default="{ row }">{{ row.confidence.toFixed(2) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }"><el-tag type="info" size="small">{{ row.status }}</el-tag></template>
        </el-table-column>
        <el-table-column label="操作" width="170" fixed="right">
          <template #default="{ row }">
            <el-button link type="success" size="small" @click="doConfirmEdge(row.id)">确认恢复</el-button>
            <el-button link type="danger" size="small" @click="doRejectEdge(row.id)">维持否决</el-button>
          </template>
        </el-table-column>
        <template #empty><el-empty description="复核队列为空" :image-size="80" /></template>
      </el-table>
    </section>

    <!-- 资料摄取 -->
    <section v-else-if="page === 'okb:ingest'" class="panel">
      <div class="panel-title">
        <div><h2>资料摄取</h2><span class="muted">脱敏工单 / 厂商文档 / 专家经验入库 → 提取候选知识；独立工单印证 ≥2 自动升 curated</span></div>
      </div>
      <el-form label-width="90px" class="okb-form" @submit.prevent>
        <el-form-item label="数据源">
          <el-select v-model="ingest.kind" class="okb-select">
            <el-option label="脱敏工单 / 复盘" value="internal_case" />
            <el-option label="厂商文档" value="vendor_doc" />
            <el-option label="专家经验" value="expert_input" />
          </el-select>
        </el-form-item>
        <el-form-item label="标题">
          <el-input v-model="ingest.title" placeholder="如 工单-2026-001 GPU ECC 复盘" />
        </el-form-item>
        <el-form-item label="正文">
          <el-input v-model="ingest.content" type="textarea" :rows="10"
            placeholder="脱敏后的工单/文档正文。当前开发提取器按 JSON 标记片段识别：&#10;{&quot;anomaly&quot;: &quot;gpu.ecc.error&quot;, &quot;name&quot;: &quot;GPU ECC 错误&quot;, &quot;code&quot;: &quot;gpu.ecc.error&quot;, &quot;alert&quot;: &quot;ALERT_GPU_ECC&quot;}&#10;{&quot;source&quot;: &quot;gpu.ecc.error&quot;, &quot;target&quot;: &quot;gpu.link.degrade&quot;}&#10;含 IP / 手机号 / 凭据等内容会被脱敏门禁拒绝（422）。" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading.ingest" @click="doIngest">摄取入库</el-button>
          <span v-if="ingestResult" class="okb-success">
            新增 {{ ingestResult.new_anomalies }} 异常 / {{ ingestResult.new_rules }} 因果边 / {{ ingestResult.new_mappings }} 告警映射<template v-if="ingestResult.curated_rules">，{{ ingestResult.curated_rules }} 边自动升 curated</template>
          </span>
        </el-form-item>
      </el-form>
    </section>

    <!-- 诊断反馈 -->
    <section v-else-if="page === 'okb:report'" class="panel">
      <div class="panel-title">
        <div><h2>诊断反馈</h2><span class="muted">上报诊断使用的边与最终判定：确认 +0.05 置信度，否决降级入复核；同一诊断引用幂等</span></div>
      </div>
      <el-form label-width="110px" class="okb-form" @submit.prevent>
        <el-form-item label="诊断引用">
          <el-input v-model="report.diagnosisRef" placeholder="本次诊断唯一引用（幂等键），如 diag-2026-001" />
        </el-form-item>
        <el-form-item label="因果边 ID">
          <el-input v-model="report.edgeId" placeholder="trace 结果或知识清单中的边 ID" />
        </el-form-item>
        <el-form-item label="判定">
          <el-radio-group v-model="report.verdict">
            <el-radio value="confirmed">确认（置信度 +0.05）</el-radio>
            <el-radio value="rejected">否决（降级入复核队列）</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="report.notes" type="textarea" :rows="2" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading.report" @click="doReport">上报反馈</el-button>
          <span v-if="reportResult" class="okb-success">
            {{ reportResult.recorded ? `已记录：置信度 ${reportResult.new_confidence.toFixed(2)}，状态 ${reportResult.new_status}` : '重复上报：幂等忽略' }}
          </span>
        </el-form-item>
      </el-form>
    </section>

    <!-- 证据回链对话框（各页共用） -->
    <el-dialog v-model="evidenceVisible" title="证据回链" width="720px">
      <div v-if="evidence" class="drawer-heading okb-evidence-head">
        <span class="muted">实体 {{ evidence.entity_type }} / {{ evidence.entity_id }}</span>
        <el-tag size="small">revision {{ evidence.revision_id?.slice(0, 8) }}</el-tag>
      </div>
      <el-empty v-if="evidence && evidence.items.length === 0" description="无证据" :image-size="60" />
      <div v-for="item in evidence?.items ?? []" :key="item.evidence_id" class="okb-evidence-item">
        <div class="okb-evidence-meta">
          <el-tag :type="levelTag(item.level)" size="small">{{ levelLabel(item.level) }}</el-tag>
          <span v-if="item.source_kind" class="muted">{{ sourceLabel(item.source_kind) }}</span>
          <span class="muted">{{ item.created_by === 'model' ? '模型提取' : '专家' }}</span>
        </div>
        <div class="table-secondary">{{ item.summary }}</div>
        <pre class="okb-evidence-content">{{ item.content }}</pre>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import { ElMessage } from 'element-plus'
import { Warning, Link, Bell, CircleCheck, ArrowRight, Refresh } from '@element-plus/icons-vue'
import * as echarts from 'echarts'
import {
  okbApi, type LookupResult, type TraceResult, type EvidenceView,
  type IngestOutput, type ReportResult, type SourceKind, type Verdict, type GraphView,
  type KnowledgeList, type KnowledgeRow, type MitigationRow, type ScenarioCard, type ScenarioDetail,
} from '../api/okb'

// page 由 App.vue 侧边栏驱动（okb:list / okb:graph / okb:query / okb:review / okb:ingest / okb:report）
const props = defineProps<{ page: string }>()
const emit = defineEmits<{ (e: 'navigate', page: string): void }>()

const loading = reactive({ lookup: false, trace: false, ingest: false, report: false, review: false, graph: false, list: false, mitigations: false })
const connectionError = ref('')

// 统一错误提示：网络失败（fetch 抛 TypeError）标记连接错误，其余弹消息
function notifyError(err: unknown) {
  const msg = err instanceof Error ? err.message : String(err)
  if (err instanceof TypeError) connectionError.value = msg
  ElMessage.error(msg)
}

function wrap<T>(key: keyof typeof loading, fn: () => Promise<T>): Promise<T | null> {
  loading[key] = true
  connectionError.value = ''
  return fn().catch((err: unknown) => { notifyError(err); return null }).finally(() => { loading[key] = false })
}

// 跨页跳转：先记上下文再上抛导航；目标数据为响应式状态，不依赖目标页 DOM
function goto(page: string, code?: string, doTraceNow = false) {
  if (code) traceCode.value = code
  emit('navigate', page)
  if (doTraceNow) doTrace()
}

// ---------- 知识清单 ----------
const listOpen = ref<string[]>(['anomaly', 'rule', 'mapping', 'review'])
const listFilter = reactive({ q: '', status: '' })
const listFiltered = computed(() => !!(listFilter.q || listFilter.status))

function clearListFilter() {
  listFilter.q = ''
  listFilter.status = ''
  void loadAllLists()
}
const listAnomaly = ref<KnowledgeList | null>(null)
const listRule = ref<KnowledgeList | null>(null)
const listMapping = ref<KnowledgeList | null>(null)
const reviewEdges = ref<KnowledgeRow[] | null>(null)

// ---------- 领域（场景组织维度） ----------
// 与后端 domainFromCode 前缀推导一致；节点按领域着色与分组展示
const DOMAIN_COLORS: Record<string, string> = {
  network: '#3479df',
  power: '#e6a23c',
  gpu: '#38a078',
  rack: '#9a67ea',
  server: '#14a0a6',
  hca: '#5a9bd4',
  pcie: '#c9743a',
  storage: '#8e7cc3',
  action: '#6ea8b8',
  other: '#a0aec0',
}

const DOMAIN_LABELS: Record<string, string> = {
  network: '网络', power: '供电', gpu: 'GPU', rack: '机柜', server: '服务器',
  hca: 'HCA 网卡', pcie: 'PCIe', storage: '存储', action: '处置动作', other: '未分类',
}

function domainLabel(d: string): string {
  return DOMAIN_LABELS[d] ?? d
}

// 异常按领域分桶（保持后端返回顺序），供清单分组渲染
const anomalyByDomain = computed<Array<{ domain: string; rows: KnowledgeRow[] }>>(() => {
  const rows = listAnomaly.value?.rows ?? []
  const buckets = new Map<string, KnowledgeRow[]>()
  for (const row of rows) {
    const key = row.category || 'other'
    if (!buckets.has(key)) buckets.set(key, [])
    buckets.get(key)!.push(row)
  }
  return Array.from(buckets, ([domain, groupRows]) => ({ domain, rows: groupRows }))
})

// 图谱领域过滤（空 = 全部）
const graphDomainFilter = ref('')

async function loadAllLists() {
  loading.list = true
  connectionError.value = ''
  const params = { q: listFilter.q || undefined, status: listFilter.status || undefined, limit: 200 }
  try {
    const [a, r, m, q] = await Promise.all([
      okbApi.knowledge('anomaly', params),
      okbApi.knowledge('rule', params),
      okbApi.knowledge('mapping', params),
      okbApi.reviewQueue().catch(() => ({ edges: [] as KnowledgeRow[] })),
    ])
    listAnomaly.value = a
    listRule.value = r
    listMapping.value = m
    reviewEdges.value = q.edges
  } catch (err: unknown) {
    notifyError(err)
  } finally {
    loading.list = false
  }
}

// ---------- 场景卡片 ----------
const listView = ref<'scenarios' | 'groups'>('scenarios')
const scenarioCards = ref<ScenarioCard[] | null>(null)
const scenarioVisible = ref(false)
const scenarioDetail = ref<ScenarioDetail | null>(null)

function edgeHealth(card: ScenarioCard): number {
  return card.edge_count === 0 ? 0 : Math.round((card.curated_edges / card.edge_count) * 100)
}

async function loadScenarioCards() {
  const res = await wrap('list', () => okbApi.scenarioCards())
  if (res) scenarioCards.value = res.scenarios
}

async function doScenarioDetail(code: string) {
  const detail = await wrap('list', () => okbApi.scenarioDetail(code))
  if (detail) { scenarioDetail.value = detail; scenarioVisible.value = true }
}

// ---------- 查询 ----------
const alertSignature = ref('')
const traceCode = ref('')
const traceHops = ref(3)
const traceIncludeDraft = ref(false)
const lookup = ref<LookupResult | null>(null)
const trace = ref<TraceResult | null>(null)

async function doLookup() {
  if (!alertSignature.value.trim()) return void ElMessage.warning('请输入告警 signature')
  lookup.value = await wrap('lookup', () => okbApi.lookup(alertSignature.value.trim(), traceIncludeDraft.value))
}

async function doTrace() {
  if (!traceCode.value.trim()) return void ElMessage.warning('请输入异常 stable_code')
  trace.value = await wrap('trace', () => okbApi.trace(traceCode.value.trim(), traceHops.value, traceIncludeDraft.value))
}

// ---------- 证据 ----------
const evidenceVisible = ref(false)
const evidence = ref<EvidenceView | null>(null)

async function doEvidence(ref: string) {
  const view = await wrap('trace', () => okbApi.evidence(ref))
  if (view) { evidence.value = view; evidenceVisible.value = true }
}

// ---------- 图谱 ----------
const graphContainer = ref<HTMLDivElement>()
const graphIncludeDraft = ref(false)
const graphLoaded = ref(false)
const graphEmpty = ref(false)
const graphLegend = ref<Array<{ label: string; color: string }>>([])
const graphDraftHiddenCount = ref(0)
const nodeDialogVisible = ref(false)
const nodeDialogNode = ref<GraphView['nodes'][number] | null>(null)
let chart: echarts.ECharts | null = null

const LEVEL_COLORS: Record<string, string> = {
  case_verified: '#38a078',
  doc_explicit: '#d79b35',
  inferred: '#8b98aa',
  expert_only: '#8e7cc3',
}

// 边类型视觉编码：trigger 实线（按证据等级着色）、mitigate 青色粗虚线、indicate 灰点线
const EDGE_STYLES: Record<string, { color: string; type: 'solid' | 'dashed' | 'dotted'; widthBoost: number }> = {
  trigger: { color: '', type: 'solid', widthBoost: 0 },
  mitigate: { color: '#14a0a6', type: 'dashed', widthBoost: 1 },
  indicate: { color: '#aeb9c9', type: 'dotted', widthBoost: 0 },
  prevent: { color: '#8e7cc3', type: 'dotted', widthBoost: 1 },
}

function edgeTypeLabel(t?: string): string {
  return ({ trigger: '因果传播', indicate: '症状表现', mitigate: '处置手段', prevent: '预防措施' } as Record<string, string>)[t ?? 'trigger'] ?? t ?? '因果传播'
}

function edgeLine(e: { edge_type?: string; status: string; evidence_level: string; confidence: number }) {
  const base = EDGE_STYLES[e.edge_type ?? 'trigger'] ?? EDGE_STYLES.trigger
  const color = base.color || LEVEL_COLORS[e.evidence_level] || '#8b98aa'
  return {
    color,
    width: 1 + Math.round(e.confidence * 3) + (base.widthBoost ?? 0),
    type: e.status === 'curated' ? base.type : 'dashed',
    curveness: 0.15,
  }
}

const lastGraphView = ref<GraphView | null>(null) // 缓存图数据：领域过滤切换时无需重新请求

// 图中出现的领域选项（动态）
const graphDomainOptions = computed<string[]>(() => {
  const view = lastGraphView.value
  if (!view) return []
  return Array.from(new Set(view.nodes.map(n => n.category || 'other')))
})

async function doGraph() {
  const view = await wrap('graph', () => okbApi.graph())
  if (!view) return
  lastGraphView.value = view
  graphEmpty.value = view.nodes.length === 0
  if (graphEmpty.value) return
  graphLoaded.value = true
  // v-show 已挂载容器；等 DOM 更新完成后初始化 echarts 并渲染
  await nextTick()
  renderGraph(view)
}

function ensureChart(): echarts.ECharts | null {
  if (!graphContainer.value) return null
  if (chart && chart.isDisposed()) chart = null
  if (!chart) chart = echarts.init(graphContainer.value)
  return chart
}

function renderGraph(view: GraphView) {
  const instance = ensureChart()
  if (!instance) return
  // 领域过滤：选中领域时只保留该领域节点及其关联边
  const domain = graphDomainFilter.value
  const filteredNodes = domain
    ? view.nodes.filter(n => (n.category || 'other') === domain)
    : view.nodes
  const nodeIds = new Set(filteredNodes.map(n => n.stable_code))
  const visibleEdges = view.edges
    .filter(e => (graphIncludeDraft.value || e.status === 'curated')
      && (!domain || (nodeIds.has(e.source) && nodeIds.has(e.target))))
  const presentLevels = new Set(visibleEdges.filter(e => e.edge_type === 'trigger').map(e => e.evidence_level))
  const presentTypes = new Set(visibleEdges.map(e => e.edge_type ?? 'trigger'))
  const presentDomains = new Set(filteredNodes.map(n => n.category || 'other'))
  graphLegend.value = Array.from(presentDomains).map(d => ({
    label: domainLabel(d), color: DOMAIN_COLORS[d] ?? DOMAIN_COLORS.other,
  }))
  graphLegend.value = graphLegend.value.concat(Array.from(presentLevels).map(level => ({
    label: levelLabel(level), color: LEVEL_COLORS[level] || '#8b98aa',
  })))
  for (const t of presentTypes) {
    if (t !== 'trigger') {
      const style = EDGE_STYLES[t]
      if (style?.color) graphLegend.value.push({ label: edgeTypeLabel(t), color: style.color })
    }
  }
  // 因草稿边被过滤而孤立的节点：提示而非隐藏（用户需要知道知识存在、只是未审核）
  const draftOnlyIds = new Set<string>()
  if (!graphIncludeDraft.value) {
    const connected = new Set<string>()
    view.edges.filter(e => e.status === 'curated').forEach(e => { connected.add(e.source); connected.add(e.target) })
    view.nodes.forEach(n => {
      const hasDraftEdge = view.edges.some(e => e.status !== 'curated' && (e.source === n.stable_code || e.target === n.stable_code))
      if (!connected.has(n.stable_code) && hasDraftEdge) draftOnlyIds.add(n.stable_code)
    })
  }
  graphDraftHiddenCount.value = draftOnlyIds.size
  instance.setOption({
    tooltip: {
      formatter: (params: { dataType: string; data: Record<string, unknown> }) => {
        const d = params.data
        if (params.dataType === 'edge') {
          return `<b>${d.source} → ${d.target}</b><br/>类型：${edgeTypeLabel(d.edge_type as string)} · 证据：${d.evidence_level} · 置信度 ${Number(d.confidence).toFixed(2)}<br/>状态：${d.status} · 印证 ${d.case_support_count} 工单`
        }
        const isolated = d.draft_only ? '<br/><span style="color:#b26205">存在未审核（草稿）关联边，勾选「显示草稿边」可见</span>' : ''
        return `<b>${d.name}</b><br/>${d.stable_code}<br/>状态：${d.status}${isolated}`
      },
    },
    series: [{
      type: 'graph', layout: 'force', roam: true, draggable: true,
      force: { repulsion: 320, edgeLength: [90, 180], gravity: 0.08 },
      label: { show: true, position: 'right', fontSize: 11, formatter: (p: { data: { name: string } }) => p.data.name },
      data: filteredNodes.map(n => {
        const draftOnly = draftOnlyIds.has(n.stable_code)
        const domainColor = DOMAIN_COLORS[n.category || 'other'] ?? DOMAIN_COLORS.other
        return {
          id: n.stable_code, name: n.name, stable_code: n.stable_code, status: n.status, draft_only: draftOnly,
          category: n.category || 'other',
          symbolSize: 34,
          // 领域色板着色；draft 半透明区分；仅草稿边关联节点保持虚线空圈提示
          itemStyle: draftOnly
            ? { color: '#f5f7fa', borderColor: '#d79b35', borderWidth: 2, borderType: 'dashed' }
            : { color: domainColor, opacity: n.status === 'curated' ? 1 : 0.45 },
          label: draftOnly ? { color: '#9aa7b8' } : undefined,
        }
      }),
      edges: visibleEdges.map(e => ({
        source: e.source, target: e.target, edge_type: e.edge_type ?? 'trigger',
        evidence_level: e.evidence_level,
        confidence: e.confidence, status: e.status, case_support_count: e.case_support_count,
        lineStyle: edgeLine(e),
      })),
      emphasis: { focus: 'adjacency' },
    }],
  })
  instance.off('click')
  instance.on('click', (params: echarts.ECElementEvent) => {
    const data = (params.data ?? {}) as { stable_code?: string }
    if (params.dataType === 'node' && data.stable_code) {
      nodeDialogNode.value = view.nodes.find(n => n.stable_code === data.stable_code) ?? null
      nodeDialogVisible.value = true
    }
  })
}

// ---------- 处置建议 ----------
const mitigations = ref<MitigationRow[]>([])

async function doMitigations(code: string) {
  mitigations.value = []
  const res = await wrap('mitigations', () => okbApi.mitigations(code, true))
  if (res) mitigations.value = res.mitigations
}

// ---------- 摄取 ----------
const ingest = reactive<{ kind: SourceKind; title: string; content: string }>({
  kind: 'internal_case', title: '', content: '',
})
const ingestResult = ref<IngestOutput | null>(null)

async function doIngest() {
  if (!ingest.title.trim() || !ingest.content.trim()) return void ElMessage.warning('标题与正文必填')
  const out = await wrap('ingest', () => okbApi.ingest({ ...ingest }))
  if (out) {
    ingestResult.value = out
    if (out.duplicate_content) {
      ElMessage.warning('该内容已入库过，本次未新增证据（印证不重复计数）')
    } else {
      ElMessage.success('摄取成功')
    }
  }
}

// ---------- 反馈 ----------
const report = reactive<{ diagnosisRef: string; edgeId: string; verdict: Verdict; notes: string }>({
  diagnosisRef: '', edgeId: '', verdict: 'confirmed', notes: '',
})
const reportResult = ref<ReportResult | null>(null)

async function doReport() {
  if (!report.diagnosisRef.trim() || !report.edgeId.trim()) return void ElMessage.warning('诊断引用与边 ID 必填')
  const out = await wrap('report', () => okbApi.report({
    diagnosis_ref: report.diagnosisRef.trim(), edge_id: report.edgeId.trim(),
    verdict: report.verdict, notes: report.notes,
  }))
  if (out) {
    reportResult.value = out
    ElMessage.success(out.recorded ? '反馈已记录' : '重复上报已忽略')
  }
}

// ---------- 复核与治理 ----------
async function doReviewQueue() {
  const res = await wrap('review', () => okbApi.reviewQueue())
  if (res) reviewEdges.value = res.edges
}

// 治理操作后同时刷新复核队列与已加载的清单，避免页面间数据滞留
async function refreshAfterGovernance() {
  await Promise.all([doReviewQueue(), listRule.value ? loadAllLists() : Promise.resolve()])
}

async function doConfirmEdge(id: string) {
  if (await wrap('review', () => okbApi.confirmEdge(id))) {
    ElMessage.success('已确认')
    void refreshAfterGovernance()
  }
}

async function doRejectEdge(id: string) {
  if (await wrap('review', () => okbApi.rejectEdge(id))) {
    ElMessage.success('已维持否决')
    void refreshAfterGovernance()
  }
}

// ---------- 展示辅助 ----------
function formatTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

function levelTag(level?: string): 'success' | 'warning' | 'info' | 'danger' {
  if (level === 'case_verified') return 'success'
  if (level === 'doc_explicit') return 'warning'
  if (level === 'expert_only') return 'danger'
  return 'info'
}

function levelLabel(level?: string): string {
  return ({ case_verified: '案例实证', doc_explicit: '文档明示', inferred: '模型推导', expert_only: '专家经验' } as Record<string, string>)[level ?? ''] ?? level ?? '—'
}

function sourceLabel(kind: SourceKind): string {
  if (kind === 'internal_case') return '工单 / 复盘'
  if (kind === 'vendor_doc') return '厂商文档'
  return '专家经验'
}

function evalSummary(hop: { evaluations?: Array<{ result: string }> }): string {
  const evals = hop.evaluations || []
  if (evals.length === 0) return '无条件'
  const unknown = evals.filter(e => e.result === 'unknown').length
  if (unknown > 0) return `${unknown} 项待验证`
  return evals.every(e => e.result === 'passed') ? '条件满足' : '条件不满足'
}

function hopClass(hop: { evaluations?: Array<{ result: string }> }): string {
  const evals = hop.evaluations || []
  if (evals.some(e => e.result === 'unknown')) return 'unknown'
  if (evals.length > 0 && evals.every(e => e.result === 'passed')) return 'passed'
  return ''
}

// ---------- 页面切换按需加载 ----------
watch(() => props.page, page => {
  if (page !== 'okb:graph' && chart) { chart.dispose(); chart = null } // 容器随 v-show 常驻，但离开仍释放实例
  if (page === 'okb:list' && !listAnomaly.value) loadAllLists()
  if (page === 'okb:list' && !scenarioCards.value) loadScenarioCards()
  if (page === 'okb:review' && !reviewEdges.value) doReviewQueue()
  if (page === 'okb:graph' && !graphLoaded.value) doGraph()
}, { immediate: true })

watch(graphIncludeDraft, () => { if (graphLoaded.value) doGraph() })
watch(graphDomainFilter, () => {
  if (graphLoaded.value && lastGraphView.value) renderGraph(lastGraphView.value)
})

onBeforeUnmount(() => { chart?.dispose(); chart = null })
</script>

<style scoped>
.okb-toolbar { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
.okb-toolbar-row { margin: 18px 0; }
.okb-search { width: 240px; } .okb-search-lg { width: 320px; } .okb-select { width: 140px; }
.okb-collapse { margin-top: 18px; border-top: 0; }
.okb-collapse :deep(.el-collapse-item__header) { font-size: 13px; }
.okb-collapse :deep(.el-collapse-item__wrap) { border-bottom: 1px solid #eef2f7; }
.okb-section-name { display: inline-flex; align-items: center; gap: 7px; font-weight: 600; margin-right: 10px; }
.okb-section-name .el-icon { color: #6c8bb1; }
.okb-arrow { color: #b8c4d4; margin: 0 6px; vertical-align: middle; }
.okb-graph-area { height: 520px; border: 1px solid #e5ebf2; border-radius: 10px;
  background: radial-gradient(circle, #d7e2f0 1px, transparent 1px) 0 0 / 22px 22px, #fafcff; }
.okb-chain { border: 1px solid #e5ebf2; border-radius: 10px; padding: 14px 16px; margin-bottom: 10px; background: #fbfcff; }
.okb-chain-head { display: flex; gap: 10px; align-items: center; margin-bottom: 10px; }
.okb-chain-hops { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.okb-hop { display: inline-flex; flex-direction: column; gap: 3px; border: 1px solid #d5e3f5; background: #f0f6ff; border-radius: 8px; padding: 7px 12px; }
.okb-hop.unknown { border-color: #f3d19e; background: #fffaf0; }
.okb-hop.passed { border-color: #bce3cd; background: #f0faf4; }
.okb-hop-name { font-size: 12px; font-weight: 600; color: #203249; }
.okb-hop-eval { font-size: 11px; color: #7b899d; }
.okb-chain-note { margin-top: 8px; color: #b26205; font-size: 11px; }
.okb-form { max-width: 760px; margin-top: 16px; }
.okb-success { margin-left: 12px; color: #38a078; font-size: 12px; }
.okb-evidence-head { display: flex; justify-content: space-between; align-items: center; margin-top: 0; }
.okb-evidence-item { border-top: 1px solid #eef2f7; padding: 12px 0; }
.okb-evidence-item:first-of-type { border-top: 0; }
.okb-evidence-meta { display: flex; gap: 8px; align-items: center; margin-bottom: 6px; }
.okb-evidence-content { background: #f6f8fc; padding: 10px; border-radius: 6px; font: 11px/1.7 "SFMono-Regular", Consolas, monospace; white-space: pre-wrap; overflow-wrap: anywhere; margin: 6px 0 0; }
.okb-mitigations { margin-top: 14px; border-top: 1px solid #eef2f7; padding-top: 10px; }
.okb-mitigations-title { margin-bottom: 8px; }
.okb-mitigation-row { display: flex; gap: 10px; align-items: center; padding: 6px 0; font-size: 12px; }
.okb-scenario-head { display: flex; gap: 10px; align-items: center; }
.okb-cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 14px; margin-top: 18px; }
.okb-card { --domain-color: #a0aec0; border: 1px solid #e5ebf2; border-top: 3px solid var(--domain-color); border-radius: 10px; padding: 14px 16px; cursor: pointer; background: #fbfcff; transition: box-shadow .15s, transform .15s; }
.okb-card:hover { box-shadow: 0 4px 14px #3479df1a; transform: translateY(-2px); }
.okb-card-head { display: flex; align-items: center; gap: 8px; }
.okb-card-dot { width: 9px; height: 9px; border-radius: 50%; background: var(--domain-color); }
.okb-card-name { font-weight: 600; font-size: 14px; color: #203249; }
.okb-card-desc { color: #7b899d; font-size: 12px; margin: 8px 0 10px; min-height: 32px; }
.okb-card-stats { display: flex; gap: 12px; flex-wrap: wrap; font-size: 12px; color: #52627a; }
.okb-card-stats b { color: #203249; }
.okb-card-health { display: flex; align-items: center; gap: 10px; margin-top: 10px; }
.okb-card-health .el-progress { flex: 1; }
</style>
