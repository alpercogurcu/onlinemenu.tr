import fs from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

import { describe, expect, it } from "vitest"

// Global Constraint (T2 plan): no page or component may paint a raw Tailwind
// palette colour for meaning — every semantic use goes through the five
// --status-* tokens (globals.css) so both themes stay in step. Walks
// src/app and src/components (excluding components/ui, which owns the
// tokens' only legitimate raw-colour definitions) and fails on any match.
const RAW_PALETTE = /\b(bg|text|border|ring|from|to)-(red|green|blue|yellow|purple|slate|sky|teal|orange|amber|emerald|rose|indigo|violet|zinc|neutral|stone|gray|lime|cyan|fuchsia|pink)-\d{2,3}\b/

const dirname = path.dirname(fileURLToPath(import.meta.url))
const SRC_ROOT = path.resolve(dirname, "..")
const ROOTS = ["app", "components"]
const EXCLUDED = [path.join("components", "ui")]

function collectTsxFiles(dir: string): string[] {
  const entries = fs.readdirSync(dir, { withFileTypes: true })
  const files: string[] = []
  for (const entry of entries) {
    const full = path.join(dir, entry.name)
    const relFromSrc = path.relative(SRC_ROOT, full)
    if (EXCLUDED.some((excluded) => relFromSrc === excluded || relFromSrc.startsWith(`${excluded}${path.sep}`))) {
      continue
    }
    if (entry.isDirectory()) {
      files.push(...collectTsxFiles(full))
    } else if (entry.isFile() && entry.name.endsWith(".tsx")) {
      files.push(full)
    }
  }
  return files
}

describe("no raw Tailwind palette classes", () => {
  it("finds no raw palette utility in src/app or src/components (excluding ui)", () => {
    const offenders: string[] = []
    for (const root of ROOTS) {
      const rootDir = path.join(SRC_ROOT, root)
      if (!fs.existsSync(rootDir)) continue
      for (const file of collectTsxFiles(rootDir)) {
        const content = fs.readFileSync(file, "utf-8")
        for (const line of content.split("\n")) {
          if (RAW_PALETTE.test(line)) {
            offenders.push(`${path.relative(SRC_ROOT, file)}: ${line.trim()}`)
          }
        }
      }
    }
    expect(offenders).toEqual([])
  })
})
