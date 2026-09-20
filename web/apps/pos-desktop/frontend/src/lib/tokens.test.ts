import { describe, expect, it } from 'vitest'
// This workspace has no @types/node, and vitest stubs `.css?raw` imports to an
// empty string, so the file is read with node:fs and the missing typings are
// acknowledged here rather than adding a dependency for one test.
// @ts-expect-error TS2307: no @types/node in this workspace
import { readFileSync } from 'node:fs'

// style.css is the single source of the palette; reading it here (instead of
// duplicating hex values) means a token edit that breaks contrast fails CI.
const css: string = readFileSync(new URL('../style.css', import.meta.url), 'utf8')

function token(name: string): string {
  const match = css.match(new RegExp(`--color-${name}:\\s*(#[0-9a-fA-F]{6})`))
  if (!match) throw new Error(`token --color-${name} not found in style.css`)
  return match[1].toLowerCase()
}

function luminance(hex: string): number {
  const channels = [1, 3, 5].map((i) => {
    const c = Number.parseInt(hex.slice(i, i + 2), 16) / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2]
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

describe('palette', () => {
  it('gives amber, warn, occupied and danger four different colors', () => {
    const colors = ['amber', 'warn', 'occupied', 'danger'].map(token)
    expect(new Set(colors).size).toBe(colors.length)
  })

  it.each([
    { name: 'ink on occupied table card', fg: 'ink', bg: 'occupied', min: 4.5 },
    { name: 'amber-ink on amber button', fg: 'amber-ink', bg: 'amber', min: 4.5 },
    { name: 'warn text on surface', fg: 'warn', bg: 'surface', min: 4.5 },
    { name: 'warn text on panel', fg: 'warn', bg: 'panel', min: 4.5 },
    { name: 'occupied border on panel', fg: 'occupied-line', bg: 'panel', min: 3 },
    { name: 'warn glyph on surface chip (pending fiscal dot over an occupied card)', fg: 'warn', bg: 'surface', min: 3 },
  ])('$name has at least $min:1 contrast', ({ fg, bg, min }) => {
    expect(contrast(token(fg), token(bg))).toBeGreaterThanOrEqual(min)
  })
})
