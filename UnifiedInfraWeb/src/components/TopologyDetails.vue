<script setup lang="ts">
import type { TopologyEdge, TopologyNode } from '../api/topologyTypes'
import { edgeLabels, entityLabels, planeLabel, propertyText, relationLabels, topologyDisplayName } from '../services/topology'
import { formatTime } from '../services/display'

defineProps<{ node?: TopologyNode; edge?: TopologyEdge; sourceName: (id: string) => string }>()
</script>

<template>
  <div class="topology-details">
    <template v-if="node || edge">
      <h2>{{ node ? '节点详情' : '关系详情' }}</h2>
      <h3 class="break">{{ node ? topologyDisplayName(node) : relationLabels[edge!.relation_kind] || edge!.relation_kind }}</h3>
      <el-descriptions :column="1" border size="small" class="spaced">
        <el-descriptions-item label="ID"><span class="mono break">{{ (node || edge)!.id }}</span></el-descriptions-item>
        <el-descriptions-item label="来源">{{ sourceName((node || edge)!.source_id) }}<div class="mono break">{{ (node || edge)!.source_id }}</div></el-descriptions-item>
        <template v-if="node">
          <el-descriptions-item label="实体类型">{{ entityLabels[node.entity_type] }}（{{ node.entity_type }}）</el-descriptions-item>
          <el-descriptions-item label="稳定 ID"><span class="mono break">{{ node.stable_id }}</span></el-descriptions-item>
        </template>
        <template v-if="edge">
          <el-descriptions-item label="关系领域">{{ edgeLabels[edge.edge_type] }}</el-descriptions-item>
          <el-descriptions-item label="关系类型">{{ relationLabels[edge.relation_kind] || edge.relation_kind }}<div class="mono">{{ edge.relation_kind }}</div></el-descriptions-item>
          <el-descriptions-item label="起点"><span class="mono break">{{ edge.from }}</span></el-descriptions-item>
          <el-descriptions-item label="终点"><span class="mono break">{{ edge.to }}</span></el-descriptions-item>
          <el-descriptions-item label="Rank"><span class="mono">{{ edge.rank }}</span></el-descriptions-item>
          <el-descriptions-item label="A/B 路">{{ propertyText(edge.properties.power_path) }}</el-descriptions-item>
          <el-descriptions-item label="平面">{{ planeLabel(edge.properties.plane) }}</el-descriptions-item>
        </template>
        <el-descriptions-item label="创建时间">{{ formatTime((node || edge)!.created_at) }}</el-descriptions-item>
        <el-descriptions-item label="同步时间">{{ formatTime((node || edge)!.synced_at) }}</el-descriptions-item>
      </el-descriptions>
      <p v-if="edge?.relation_kind === 'links_to'" class="page-footnote">物理互联箭头仅表示图库存储方向，不代表流量方向。</p>
      <h3 class="spaced">原始属性</h3>
      <dl v-if="Object.keys((node || edge)!.properties).length" class="topology-properties"><template v-for="(value, key) in (node || edge)!.properties" :key="key"><dt>{{ key }}</dt><dd><pre>{{ propertyText(value) }}</pre></dd></template></dl>
      <p v-else class="muted">该对象没有额外属性。</p>
    </template>
    <el-empty v-else description="选择节点或关系查看真实属性" :image-size="72" />
  </div>
</template>
