import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import { VitePWA } from 'vite-plugin-pwa'
import { computeBuildId } from './build-id.ts'

// Dev/watch only. Production ids come from the git worktree (see build-id.ts);
// a process-local counter would stay `.1` on every install.
let buildCount = 0
let withCounter = false

function buildIdPlugin(): Plugin {
  const virtualId = 'virtual:build-id'
  const resolvedId = '\0' + virtualId
  let root = process.cwd()
  return {
    name: 'build-id',
    configResolved(config) {
      root = config.root
      withCounter = config.command === 'serve' || config.build.watch != null
    },
    resolveId(id) {
      if (id === virtualId) return resolvedId
    },
    load(id) {
      if (id !== resolvedId) return
      const counter = withCounter ? ++buildCount : undefined
      return `export const buildId = ${JSON.stringify(computeBuildId(root, counter))}`
    },
  }
}

export default defineConfig({
  plugins: [
    vue(),
    buildIdPlugin(),
    VitePWA({
      // 'prompt' installs an update in the background and leaves it waiting.
      // autoUpdate called skipWaiting + clientsClaim, and App.vue reloaded on
      // controllerchange — the page painted, then refreshed again and dropped
      // whatever the user had just opened. The waiting worker takes over the
      // next time the app is opened with no client still running.
      registerType: 'prompt',
      // notificationclick handling lives in public/sw-notify.js
      workbox: {
        importScripts: ['sw-notify.js'],
        navigateFallbackDenylist: [/^\/preview\//, /^\/api\//, /^\/file/, /^\/ws/, /^\/healthz/],
      },
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
      '/upload': 'http://localhost:8766',
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
