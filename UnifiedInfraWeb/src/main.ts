import { createApp } from 'vue'
import {
  ElAlert, ElButton, ElCheckbox, ElCollapse, ElCollapseItem, ElConfigProvider, ElDescriptions, ElDescriptionsItem,
  ElDialog, ElDrawer, ElEmpty, ElForm, ElFormItem, ElIcon, ElInput, ElInputNumber, ElLoading, ElOption,
  ElProgress, ElRadio, ElRadioButton, ElRadioGroup, ElSelect, ElSkeleton,
  ElTable, ElTableColumn, ElTag,
} from 'element-plus'
import 'element-plus/dist/index.css'
import './styles.css'
import App from './App.vue'

const app = createApp(App)
const components = {
  ElAlert, ElButton, ElCheckbox, ElCollapse, ElCollapseItem, ElConfigProvider, ElDescriptions, ElDescriptionsItem,
  ElDialog, ElDrawer, ElEmpty, ElForm, ElFormItem, ElIcon, ElInput, ElInputNumber, ElOption,
  ElProgress, ElRadio, ElRadioButton, ElRadioGroup, ElSelect, ElSkeleton, ElTable, ElTableColumn, ElTag,
}
for (const [name, component] of Object.entries(components)) app.component(name, component)
app.use(ElLoading).mount('#app')
