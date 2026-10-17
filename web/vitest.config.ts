import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

// Dedicated vitest config: component tests run in jsdom with the Vue plugin,
// without the PWA/build-specific options from vite.config.ts.
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      // vite-only virtual module (SettingsPanel imports it); stub in tests
      'virtual:build-id': fileURLToPath(new URL('./src/__mocks__/virtual-build-id.ts', import.meta.url)),
      // vite-plugin-pwa virtual module (App.vue imports it); stub in tests
      'virtual:pwa-register/vue': fileURLToPath(
        new URL('./src/__mocks__/virtual-pwa-register-vue.ts', import.meta.url),
      ),
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['src/test-setup.ts'],
    include: ['src/**/*.test.ts', 'build-id.test.ts'],
  },
})
