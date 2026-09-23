import { defineConfig } from 'vitest/config'

// Node environment, no jsdom and no setup file: everything in this package is
// deliberately pure TS (cart/options/numpad/money/payment-plan rules) with no
// React and no DOM. See src/index.ts for the package-level rationale.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
})
