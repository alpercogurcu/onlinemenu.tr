import {defineConfig} from 'vite'
import react from '@vitejs/plugin-react'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    // Default Vite output dir is `dist`, but the repo root .gitignore has
    // a blanket `dist/` rule that ignores every directory named `dist`
    // anywhere in the tree (no per-project override is possible via a
    // nested .gitignore negation once a parent pattern excludes a whole
    // directory). main.go embeds this directory (`//go:embed
    // all:frontend/web-build`) and needs a committed placeholder so a
    // fresh clone still compiles with plain `go build` before any
    // frontend build has run — hence the non-default output dir name.
    outDir: 'web-build',
  },
})
