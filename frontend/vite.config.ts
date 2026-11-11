import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 本地开发：/api 与 /healthz 代理到集群引擎（生产由 nginx 反代，见 nginx.conf）
const ENGINE = process.env.MP_BASE ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [
    react({
      babel: {
        // React Compiler：全项目自动记忆化（组件/闭包按 props 依赖缓存）——未来组件零
        // memo/useCallback 纪律。React 18 无内置编译期运行时，target '18' 走
        // react-compiler-runtime 垫片（编辑器更新式契约让 handler 依赖收敛到稳定坐标，
        // 编译器的兄弟跳过重渲才真正生效）
        plugins: [['babel-plugin-react-compiler', { target: '18' }]],
      },
    }),
  ],
  server: {
    proxy: {
      '/api': { target: ENGINE, changeOrigin: true, secure: false },
      '/healthz': { target: ENGINE, changeOrigin: true, secure: false },
    },
  },
  build: {
    outDir: 'dist',
    chunkSizeWarningLimit: 1500, // antd 单包偏大，暂不做拆包优化
  },
})
