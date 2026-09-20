import { readFileSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

import { describe, expect, it } from "vitest"

import {
  IDENTITY_TYPES,
  OPERATION_TYPES,
  OWNERSHIP_TYPES,
  operationLabel,
  ownershipLabel,
} from "@/lib/branch-options"

const migrations = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../../../../../backend/migrations/tenant",
)

// Values inside `<column> IN ( 'a', 'b', … )` of one migration file.
function checkValues(file: string, column: string): string[] {
  const sql = readFileSync(path.join(migrations, file), "utf8")
  const match = sql.match(new RegExp(`${column}\\s+IN\\s*\\(([^)]*)\\)`))
  if (!match) throw new Error(`${file}: no CHECK list for ${column}`)
  return [...match[1].matchAll(/'([^']+)'/g)].map((m) => m[1])
}

describe("şube formu seçenekleri backend CHECK kısıtlarıyla hizalı", () => {
  it("operation_type seçenekleri branches_operation_type_check içinde", () => {
    const allowed = checkValues("000008_branches_operation_type_expand.up.sql", "operation_type")
    for (const option of OPERATION_TYPES) expect(allowed).toContain(option.value)
  })

  it("ownership_type seçenekleri branches_ownership_type_check içinde", () => {
    const allowed = checkValues("000002_add_legal_and_documents.up.sql", "ownership_type")
    for (const option of OWNERSHIP_TYPES) expect(allowed).toContain(option.value)
  })

  it("identity_type seçenekleri branches_identity_type_check içinde", () => {
    const allowed = checkValues("000003_branch_details_hours_integrators.up.sql", "identity_type")
    for (const option of IDENTITY_TYPES) expect(allowed).toContain(option.value)
  })

  it("pilotun istediği dört işletme türü formda var", () => {
    expect(OPERATION_TYPES.map((o) => o.value)).toEqual(["restoran", "kafe", "fast_food", "bulut_mutfak"])
  })

  it("etiketler: bilinen değer Türkçe, formda olmayan geçerli değer de etiketli, boş tire", () => {
    expect(operationLabel("fast_food")).toBe("Fast Food")
    expect(operationLabel("food_truck")).toBe("Food Truck")
    expect(operationLabel(undefined)).toBe("—")
    expect(ownershipLabel("franchise")).toBe("Franchise")
  })
})
