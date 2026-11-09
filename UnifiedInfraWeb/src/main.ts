import { createApp } from 'vue'
import {
  ElAlert, ElButton, ElConfigProvider, ElDescriptions, ElDescriptionsItem, ElDialog, ElDrawer, ElEmpty,
  ElForm, ElFormItem, ElIcon, ElInput, ElLoading, ElOption, ElProgress, ElSelect, ElSkeleton,
  ElTable, ElTableColumn, ElTag,
} from 'element-plus'
import 'element-plus/dist/index.css'
import './styles.css'
import App from './App.vue'

const app = createApp(App)
const components = {
  ElAlert, ElButton, ElConfigProvider, ElDescriptions, ElDescriptionsItem, ElDialog, ElDrawer, ElEmpty,
  ElForm, ElFormItem, ElIcon, ElInput, ElOption, ElProgress, ElSelect, ElSkeleton, ElTable, ElTableColumn, ElTag,
}
for (const [name, component] of Object.entries(components)) app.component(name, component)
app.use(ElLoading).mount('#app')
