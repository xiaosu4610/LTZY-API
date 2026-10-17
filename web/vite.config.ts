import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.dirname(fileURLToPath(import.meta.url));
const nm = (...p: string[]) => path.join(root, 'node_modules', ...p);

// LTZY-API 前端构建配置
// 产物输出到 web/build/default，由 Go 的 //go:embed web/build/* 打进二进制。
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: [
      { find: /^@\//, replacement: path.join(root, 'src') + '/' },
      // recharts 3.x 直接引用 es-toolkit 的 dist 深层路径，而该包 exports 未导出它们，
      // 这里显式指回真实文件位置。
      { find: /^es-toolkit\/dist\/(.*)$/, replacement: nm('es-toolkit', 'dist') + '/$1' },
    ],
  },
  build: {
    outDir: '../build/default',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1200,
    rollupOptions: {
      output: {
        // Vite 8（rolldown）要求函数式 manualChunks
        manualChunks: (id: string) => {
          if (!id.includes('node_modules')) return undefined;
          if (id.includes('recharts') || id.includes('d3-') || id.includes('victory-vendor'))
            return 'charts';
          if (id.includes('@tanstack')) return 'router';
          if (id.includes('react-dom') || id.includes('/react/')) return 'react';
          return undefined;
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://127.0.0.1:3000', changeOrigin: true },
      '/v1': { target: 'http://127.0.0.1:3000', changeOrigin: true },
    },
  },
});
