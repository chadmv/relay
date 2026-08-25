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
    // configDefaults.exclude is SPREAD, never replaced. Setting `exclude` at all
    // overwrites vitest's defaults wholesale, and dropping `**/node_modules/**`
    // makes vitest walk web/node_modules collecting every *.spec.js it finds.
    // The guard on that is acceptance criterion 7: `npm test`'s collected file
    // count must be unchanged from HEAD, which a node_modules walk explodes.
    exclude: [...configDefaults.exclude, 'e2e/**'],
  },
})
