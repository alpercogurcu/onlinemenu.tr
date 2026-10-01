import { defineConfig } from 'vitest/config'

// Node environment, no jsdom and no setup file — unlike apps/admin's config.
// Everything under test here is either pure logic (lib/*, hook helpers) or a
// presentational component rendered with react-dom/server's
// renderToStaticMarkup, which needs no DOM. Pulling in jsdom would add a
// heavy dependency for nothing. Tests must also stay importable WITHOUT the
// generated (gitignored) wailsjs bindings — CI runs vitest without a wails
// toolchain — so a component under test may not transitively import
// wailsjs/go/main/App (type-only imports of the models are fine, they erase).
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.{ts,tsx}'],
  },
})
