import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';

export default defineConfig({
  plugins: [vue()],
  build: { assetsDir: 'console-assets' },
  server: { proxy: { '/api': 'http://127.0.0.1:8084', '/media': 'http://127.0.0.1:8084', '/assets': 'http://127.0.0.1:8084' } },
});
