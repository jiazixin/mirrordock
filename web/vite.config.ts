import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// build.target matches the browser support in docs/IMPLEMENTATION.md.
export default defineConfig({
  plugins: [vue()],
  base: '/admin/',
  server: { port: 5173, proxy: { '/api': 'http://127.0.0.1:8080' } },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: ['chrome124', 'edge124', 'firefox125', 'safari17.4'],
    sourcemap: false,
  },
})
