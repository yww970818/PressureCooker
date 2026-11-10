import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
export default defineConfig({ plugins: [vue()], base: '/', server: { proxy: { '/api': 'http://127.0.0.1:18090' } }, test: { environment: 'node', maxWorkers: 2 } });
