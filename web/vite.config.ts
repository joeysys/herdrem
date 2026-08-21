import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import legacy from '@vitejs/plugin-legacy'
import { VitePWA } from 'vite-plugin-pwa'
import { execSync } from 'node:child_process'

// Build ID plugin: generates a fresh ID on every rebuild via a virtual module.
// In watch mode, the `load` hook re-runs on each rebuild, so the counter
// increments and the page can tell whether it picked up the latest changes.
let buildCount = 0

function buildIdPlugin(): Plugin {
  const virtualId = 'virtual:build-id'
  const resolvedId = '\0' + virtualId
  return {
    name: 'build-id',
    resolveId(id) {
      if (id === virtualId) return resolvedId
    },
    load(id) {
      if (id !== resolvedId) return
      buildCount++
      let gitId = 'dev'
      try {
        gitId = execSync('git rev-parse --short HEAD', { encoding: 'utf-8' }).trim()
      } catch { /* not a git repo */ }
      return `export const buildId = ${JSON.stringify(`${gitId}.${buildCount}`)}`
    },
  }
}

export default defineConfig({
  plugins: [
    vue(),
    legacy({
      targets: ['chrome >= 50', 'safari >= 10', 'firefox >= 50', 'edge >= 15'],
      renderLegacyChunks: true,
    }),
    buildIdPlugin(),
    VitePWA({
      registerType: 'autoUpdate',
      manifest: {
        name: 'Herdrem',
        short_name: 'Herdrem',
        description: 'Monitor and approve herdr agents from your phone',
        display: 'standalone',
        orientation: 'portrait',
        background_color: '#0a0e13',
        theme_color: '#0a0e13',
        icons: [
          { src: '/icon-192.png', sizes: '192x192', type: 'image/png' },
          { src: '/icon-512.png', sizes: '512x512', type: 'image/png' },
          { src: '/icon-maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
        ],
      },
    }),
  ],
  server: {
    proxy: {
      '/ws': {
        target: 'ws://localhost:8766',
        ws: true,
      },
      '/healthz': 'http://localhost:8766',
      '/push': 'http://localhost:8766',
      '/file': 'http://localhost:8766',
      '/api': 'http://localhost:8766',
      '/preview': 'http://localhost:8766',
      '/preview-auth': 'http://localhost:8766',
    },
  },
  build: {
    rollupOptions: {
      input: 'index.html',
    },
    outDir: '../webdist',
    emptyOutDir: false, // preserve embed.go in webdist/
  },
})
