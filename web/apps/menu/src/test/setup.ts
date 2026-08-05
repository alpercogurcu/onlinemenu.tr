// Vitest global test setup — jest-dom matcher'larını (toBeInTheDocument vb.)
// vitest'in `expect`'ine ekler. `vitest.config.ts`'teki `test.setupFiles`
// üzerinden her test dosyasından önce yüklenir.
import "@testing-library/jest-dom/vitest"

import { webcrypto } from "node:crypto"

import { cleanup } from "@testing-library/react"
import { afterEach } from "vitest"

afterEach(() => {
  cleanup()
})

// Node 25 kendi `globalThis.localStorage`'ını tanımlıyor ve bu, `--localstorage-file`
// verilmediğinde çalışmayan bir stub ("storage.setItem is not a function").
// Global olarak tanımlı olduğu için jsdom kendi çalışan implementasyonunu ÜZERİNE
// yazmıyor — zustand'ın persist middleware'i de bu stub'a düşüyor. Sepet
// localStorage'da saklandığından (lib/cart-store.ts) testlerin çalışması için
// bellek-içi bir uygulama bağlanır. Tarayıcıda yerel API kullanılır.
if (typeof globalThis.localStorage?.setItem !== "function") {
  const memory = new Map<string, string>()
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
      getItem: (key: string) => memory.get(key) ?? null,
      setItem: (key: string, value: string) => void memory.set(key, String(value)),
      removeItem: (key: string) => void memory.delete(key),
      clear: () => memory.clear(),
      key: (index: number) => [...memory.keys()][index] ?? null,
      get length() {
        return memory.size
      },
    } satisfies Storage,
  })
}

// Idempotency-Key üretimi crypto.randomUUID'e dayanıyor (lib/cart-store.ts).
// jsdom'un window.crypto'su bunu her sürümde sağlamıyor — Node'un webcrypto'su
// bağlanır. Yalnız test sürecini etkiler; tarayıcıda yerel API kullanılır.
if (typeof globalThis.crypto?.randomUUID !== "function") {
  Object.defineProperty(globalThis, "crypto", { value: webcrypto, configurable: true })
}
