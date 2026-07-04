// Vitest global test setup — jest-dom matcher'larını (toBeInTheDocument vb.)
// vitest'in `expect`'ine ekler. `vitest.config.ts`'teki `test.setupFiles`
// üzerinden her test dosyasından önce yüklenir.
import "@testing-library/jest-dom/vitest"

import { cleanup } from "@testing-library/react"
import { afterEach } from "vitest"

// `test.globals: false` kullanıldığından (bkz. vitest.config.ts) RTL'nin
// otomatik `afterEach` tabanlı cleanup'ı devreye girmiyor — manuel bağlanır.
afterEach(() => {
  cleanup()
})
