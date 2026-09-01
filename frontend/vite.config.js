import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    // 开发环境代理：/api 与 /uploads 均转后端，避免跨域。
    // /uploads 必须代理：头像 URL 是相对路径，dev server 自身没有这些
    // 文件（在 Go 后端 upload.dir / 容器 avatar-uploads 卷里），
    // 不代理则头像 404 显示兜底图。target 与运行中的后端一致：
    // 容器栈 8080 / 原生 go run（config.yaml port）改成对应端口
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
      '/uploads': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
})
