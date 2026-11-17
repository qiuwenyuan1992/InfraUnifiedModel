import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), 'VITE_')
  return {
    plugins: [vue()],
    build: {
      rollupOptions: {
        output: {
          // echarts 体积大且仅图谱页使用，单独成块避免拖大 OkbConsole chunk
          manualChunks: { echarts: ['echarts'] },
        },
      },
    },
    server: {
      host: '127.0.0.1',
      proxy: {
        '/v1': { target: env.VITE_API_PROXY_TARGET || 'http://127.0.0.1:8000', changeOrigin: true },
        // OKB 故障知识图谱后端（独立服务，REST /api/v1/*）
        '/api': { target: env.VITE_OKB_PROXY_TARGET || 'http://127.0.0.1:18090', changeOrigin: true },
      },
    },
    preview: { host: '127.0.0.1' },
  }
})
