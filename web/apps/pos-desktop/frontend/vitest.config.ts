import { defineConfig } from 'vitest/config'

// Node environment, no jsdom and no setup file — unlike apps/admin's config.
// The only thing under test here is lib/fiscalStatus.ts, which is deliberately
// pure money arithmetic with no React and no DOM (see that module's doc
// comment). Pulling in jsdom would add a heavy dependency for nothing.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
})
