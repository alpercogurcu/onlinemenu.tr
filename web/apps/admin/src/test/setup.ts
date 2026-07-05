// Vitest global test setup — jest-dom matcher'larını (toBeInTheDocument vb.)
// vitest'in `expect`'ine ekler. `vitest.config.ts`'teki `test.setupFiles`
// üzerinden her test dosyasından önce yüklenir.
import "@testing-library/jest-dom/vitest"

import { webcrypto } from "node:crypto"

import { cleanup } from "@testing-library/react"
import { afterEach } from "vitest"

// `test.globals: false` kullanıldığından (bkz. vitest.config.ts) RTL'nin
// otomatik `afterEach` tabanlı cleanup'ı devreye girmiyor — manuel bağlanır.
afterEach(() => {
  cleanup()
})

// jsdom's `window.crypto` implements getRandomValues but not `.subtle` (no
// Web Crypto SubtleCrypto support: https://github.com/jsdom/jsdom/issues/1612).
// lib/pkce.ts relies on crypto.subtle.digest for the S256 code_challenge, so
// tests need Node's webcrypto instead. This only affects the vitest process —
// production code always runs in a real browser, which has `.subtle` natively.
if (!globalThis.crypto?.subtle) {
  Object.defineProperty(globalThis, "crypto", { value: webcrypto, configurable: true })
}
