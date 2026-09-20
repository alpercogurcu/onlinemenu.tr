"use client"

import { ShieldAlert } from "lucide-react"
import { useTranslations } from "next-intl"

import { BranchPricing } from "@/components/catalog/branch-pricing"
import { Card, CardContent } from "@/components/ui/card"
import { useCan } from "@/hooks/use-can"

// Manager-only (ADR-DATA-009 §6). The gate is cosmetic like every client-side
// permission check (lib/permissions.ts): the backend 403s the writes anyway,
// this only keeps a non-owner from landing on a screen whose every request
// would fail — and, by not mounting <BranchPricing/>, from firing them.
export default function BranchPricingPage() {
  const t = useTranslations("catalog.branchPricing.forbidden")
  const canManage = useCan("catalog.branch_override.manage")

  if (!canManage) {
    return (
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-16 text-center">
          <ShieldAlert className="mb-4 size-12 text-muted-foreground" />
          <h1 className="text-lg font-semibold">{t("title")}</h1>
          <p className="mt-1 text-sm text-muted-foreground">{t("body")}</p>
        </CardContent>
      </Card>
    )
  }

  return <BranchPricing />
}
