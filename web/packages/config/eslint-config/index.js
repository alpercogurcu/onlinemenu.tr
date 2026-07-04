"use strict"

/**
 * @onlinemenu/config/eslint — paylaşımlı ESLint temel yapılandırması.
 *
 * Bu dosya framework-agnostiktir (plugin bağımlılığı yoktur) — pnpm workspace
 * içindeki her app (apps/admin, ileride apps/pos-desktop) kendi flat config
 * dosyasında `base`'i spread eder ve framework'e özgü config'i (next/core-web-vitals,
 * eslint-plugin-react vb.) kendisi ekler.
 *
 * Kaynak: docs/lessons-from-b2b.md madde 4 — "Rol/permission kontrolleri tek
 * helper'da toplansın; component'lerde inline `role === 'admin'` yasak."
 * b2b'de bu kural yalnızca CLAUDE.md'de yazıyordu, hiçbir lint zorlamıyordu.
 * Burada `no-restricted-syntax` ile CI'da fiilen kırılıyor.
 */

const NO_INLINE_ROLE_CHECK_MESSAGE =
  "Inline rol/permission karşılaştırması yasak (docs/lessons-from-b2b.md #4). " +
  "Paylaşılan permission helper'ını kullanın (ör. hasPermission(), useHasRole()) — " +
  "component içinde `role === '...'` veya `roles.includes('...')` literal karşılaştırması yazmayın."

/**
 * Bu selector'lar kasıtlı olarak yaklaşık (approximate) tutuldu: template literal
 * veya dolaylı değişken üzerinden yapılan karşılaştırmaları yakalamaz. Amaç, en
 * yaygın b2b regresyonunu (doğrudan string literal karşılaştırması) engellemektir.
 */
const noInlineRoleCheckRules = [
  {
    // role === 'admin'  |  role !== 'admin'
    selector:
      "BinaryExpression[operator=/^(===|==|!==|!=)$/][right.type='Literal'][left.name=/^(role|roles)$/i]",
    message: NO_INLINE_ROLE_CHECK_MESSAGE,
  },
  {
    // user.role === 'admin'  |  session.roles === 'admin'
    selector:
      "BinaryExpression[operator=/^(===|==|!==|!=)$/][right.type='Literal'][left.property.name=/^(role|roles)$/i]",
    message: NO_INLINE_ROLE_CHECK_MESSAGE,
  },
  {
    // roles.includes('admin')
    selector:
      "CallExpression[callee.property.name='includes'][arguments.0.type='Literal'][callee.object.name=/^(role|roles)$/i]",
    message: NO_INLINE_ROLE_CHECK_MESSAGE,
  },
  {
    // user.roles.includes('admin')
    selector:
      "CallExpression[callee.property.name='includes'][arguments.0.type='Literal'][callee.object.property.name=/^(role|roles)$/i]",
    message: NO_INLINE_ROLE_CHECK_MESSAGE,
  },
]

/** @type {import("eslint").Linter.Config[]} */
const base = [
  {
    ignores: [
      "**/node_modules/**",
      "**/.next/**",
      "**/dist/**",
      "**/build/**",
      "**/coverage/**",
      "**/.turbo/**",
      "**/*.gen.ts",
      "**/*.gen.tsx",
    ],
  },
  {
    rules: {
      "no-restricted-syntax": ["error", ...noInlineRoleCheckRules],
    },
  },
]

module.exports = {
  base,
  noInlineRoleCheckRules,
}
