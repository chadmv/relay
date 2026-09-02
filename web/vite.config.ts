/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import { configDefaults } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/v1': 'http://localhost:8080',
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/test/setup.ts',
    css: true,
    // The Playwright specs are *.spec.ts inside vitest's root, so vitest's
    // default include matches them and its default exclude does not - they would
    // be collected and run in jsdom, where `page` does not exist.
    //
    // configDefaults.exclude is SPREAD, never replaced. The default exclude is
    // node_modules and .git only, so this spread is the whole node_modules guard
    // rather than one entry of a broad list: setting `exclude` without it sends
    // vitest walking web/node_modules collecting every *.spec.js it finds. The
    // guard on that is that `npm test`'s collected file count must not move,
    // which a node_modules walk explodes.
    exclude: [...configDefaults.exclude, 'e2e/**'],
    fakeTimers: {
      // Faking `performance` (vitest's default toFake list includes it) freezes
      // the clock React 18's scheduler reads from, inside every test that calls
      // vi.useFakeTimers() - this list holds that surface to exactly the
      // JS-timer primitives, so behaviour under fake timers stays
      // what the existing suite pins. Widening this list is a deliberate
      // change and needs its own test.
      toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'setImmediate', 'clearImmediate', 'Date'],
    },
  },
})
