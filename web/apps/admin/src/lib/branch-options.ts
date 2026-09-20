// Value sets the branch form may send. They mirror the CHECK constraints on
// `branches` (backend/migrations/tenant: operation_type in 000008,
// ownership_type in 000002, identity_type in 000003) and the service-layer
// validateBranch — anything else is answered 422. test/branch-options.test.ts
// reads those migrations, so a drift fails there instead of as a 500 on
// "Şube Ekle".
export interface Option {
  value: string
  label: string
}

export const OPERATION_TYPES: Option[] = [
  { value: "restoran", label: "Restoran" },
  { value: "kafe", label: "Kafe" },
  { value: "fast_food", label: "Fast Food" },
  { value: "bulut_mutfak", label: "Bulut Mutfak" },
]

// Values the backend accepts but the form does not offer (they still render
// with a proper label in the list).
const OTHER_OPERATION_LABELS: Record<string, string> = {
  bar: "Bar",
  market: "Market",
  food_truck: "Food Truck",
  imalat: "İmalat",
  depo: "Depo",
}

export const OWNERSHIP_TYPES: Option[] = [
  { value: "sube", label: "Şube" },
  { value: "franchise", label: "Franchise" },
]

export const IDENTITY_TYPES: Option[] = [
  { value: "kurumsal", label: "Kurumsal" },
  { value: "bireysel", label: "Bireysel" },
]

export function operationLabel(value: string | undefined): string {
  if (!value) return "—"
  return OPERATION_TYPES.find((o) => o.value === value)?.label ?? OTHER_OPERATION_LABELS[value] ?? value
}

export function ownershipLabel(value: string | undefined): string {
  if (!value) return "—"
  return OWNERSHIP_TYPES.find((o) => o.value === value)?.label ?? value
}
