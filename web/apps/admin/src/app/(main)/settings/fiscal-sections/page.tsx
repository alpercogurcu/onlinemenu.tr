"use client"

import { AlertTriangle, ListTree } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Select, SelectItem } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useCategories } from "@/hooks/use-catalog"
import {
  useFiscalSectionMappings,
  useFiscalSections,
  useFiscalTerminals,
  useReplaceFiscalSectionMappings,
} from "@/hooks/use-fiscal"
import { useSelectedBranch } from "@/hooks/use-selected-branch"

// Category -> device VAT section id. `undefined` means "not mapped yet".
type MappingState = Record<string, number | undefined>

function mappingsToState(rows: { category_id: string; section_no: number }[]): MappingState {
  const state: MappingState = {}
  for (const row of rows) state[row.category_id] = row.section_no
  return state
}

function mappingsEqual(a: MappingState, b: MappingState): boolean {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)])
  for (const key of keys) {
    if (a[key] !== b[key]) return false
  }
  return true
}

export default function FiscalSectionMappingPage() {
  // Branch comes from the global header switcher (use-selected-branch).
  // A header branch switch while the mapping table is dirty can no longer
  // be intercepted page-locally; unsaved edits are simply replaced by the
  // new branch's rows (the beforeunload guard below still covers tab
  // close/refresh).
  const { branchId } = useSelectedBranch()
  const [terminalId, setTerminalId] = useState("")

  const { data: terminalsData } = useFiscalTerminals(branchId)
  const terminals = terminalsData ?? []

  useEffect(() => {
    setTerminalId((current) => {
      if (current && terminals.some((t) => t.id === current)) return current
      return terminals[0]?.id ?? ""
    })
    // Re-run whenever the branch changes (new terminal list arrives for it).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [branchId, terminalsData])

  const { data: categoriesData, isLoading: categoriesLoading } = useCategories()
  const categories = categoriesData ?? []

  const { data: sectionsData, isLoading: sectionsLoading } = useFiscalSections(terminalId)
  const sections = sectionsData ?? []

  const { data: mappingsData, isLoading: mappingsLoading } = useFiscalSectionMappings(branchId)
  const replaceMappings = useReplaceFiscalSectionMappings()

  const [mappings, setMappings] = useState<MappingState>({})
  const [baseline, setBaseline] = useState<MappingState>({})
  const loadedForBranchRef = useRef<string | null>(null)

  // Sync local editable state from the server exactly once per branch
  // selection — not on every background refetch, which would silently wipe
  // in-progress, unsaved edits (react-query refetches on window focus).
  useEffect(() => {
    if (mappingsData && loadedForBranchRef.current !== branchId) {
      const initial = mappingsToState(mappingsData)
      setMappings(initial)
      setBaseline(initial)
      loadedForBranchRef.current = branchId
    }
  }, [mappingsData, branchId])

  const isDirty = !mappingsEqual(mappings, baseline)

  // Tab close / refresh guard. In-app navigation (sidebar links, back button)
  // is not intercepted here — Next.js App Router has no built-in router
  // guard, and adding a global one is out of scope for this page.
  useEffect(() => {
    if (!isDirty) return
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      e.returnValue = ""
    }
    window.addEventListener("beforeunload", handler)
    return () => window.removeEventListener("beforeunload", handler)
  }, [isDirty])

  const handleMapChange = (categoryId: string, value: string) => {
    setMappings((m) => ({ ...m, [categoryId]: value ? Number(value) : undefined }))
  }

  const handleSave = async () => {
    const payload = Object.entries(mappings)
      .filter((entry): entry is [string, number] => entry[1] !== undefined)
      .map(([category_id, section_no]) => ({ category_id, section_no }))

    try {
      await replaceMappings.mutateAsync({ branch_id: branchId, mappings: payload })
      toast.success("Kısım eşlemeleri kaydedildi")
      setBaseline(mappings)
    } catch {
      toast.error("Kısım eşlemeleri kaydedilemedi", {
        action: { label: "Tekrar dene", onClick: () => handleSave() },
      })
    }
  }

  const unmappedCount = categories.filter((c) => mappings[c.id] === undefined).length
  const sectionByNo = new Map(sections.map((s) => [s.section_no, s]))

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Kısım Eşleme</h1>
          <p className="text-muted-foreground">
            Katalog kategorilerini mali cihazın KDV kısımlarına eşleyin.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Select
            value={terminalId}
            onChange={(e) => setTerminalId(e.target.value)}
            className="w-48"
            aria-label="Terminal seçin"
            disabled={terminals.length === 0}
          >
            <SelectItem value="">Terminal seçin</SelectItem>
            {terminals.map((t) => (
              <SelectItem key={t.id} value={t.id}>
                {t.label || t.terminal_serial}
              </SelectItem>
            ))}
          </Select>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Senkronlu Kısımlar</CardTitle>
          <CardDescription>
            Seçili terminalin en son senkronize edilen KDV kısımları.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {!terminalId ? (
            <p className="text-sm text-muted-foreground">
              Kısımları görmek için bir terminal seçin.
            </p>
          ) : sectionsLoading ? (
            <div className="flex gap-2">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-6 w-24" />
              ))}
            </div>
          ) : sections.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              Bu terminal için kısımlar henüz senkronize edilmemiş. Fiscal Terminaller ekranından
              &quot;Kısımları Senkronla&quot; ile senkronize edin.
            </p>
          ) : (
            <div className="flex flex-wrap gap-2">
              {sections.map((s) => (
                <Badge key={s.section_no} variant="outline" className="bg-muted">
                  {s.name} · KDV %{(s.tax_permyriad / 100).toFixed(0)}
                </Badge>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {unmappedCount > 0 && (
        <div className="flex items-center gap-2 rounded-md border border-status-warning-border bg-status-warning-bg px-4 py-3 text-sm text-status-warning-fg">
          <AlertTriangle className="size-4 shrink-0" />
          <span>
            {unmappedCount} kategori eşlenmemiş — bu kategorilerde satış mali kayda gönderilemez.
          </span>
        </div>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Kategori → Kısım Eşleme</CardTitle>
          <CardDescription>Toplam {categories.length} kategori.</CardDescription>
        </CardHeader>
        <CardContent>
          {categoriesLoading || mappingsLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-12 w-full" />
              ))}
            </div>
          ) : categories.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <ListTree className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">Kategori bulunamadı</h3>
              <p className="text-sm text-muted-foreground mt-1">
                Eşleme yapabilmek için önce Katalog &gt; Kategoriler ekranından kategori ekleyin.
              </p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Kategori</TableHead>
                  <TableHead>Kısım</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {categories.map((cat) => {
                  const mappedSectionNo = mappings[cat.id]
                  const mappedSection = mappedSectionNo ? sectionByNo.get(mappedSectionNo) : undefined
                  return (
                    <TableRow key={cat.id}>
                      <TableCell className="font-medium">
                        <span className="flex items-center gap-2">
                          {mappedSectionNo === undefined && (
                            <AlertTriangle
                              className="size-4 shrink-0 text-status-warning-fg"
                              aria-label="Eşlenmemiş kategori"
                            />
                          )}
                          {cat.name}
                        </span>
                      </TableCell>
                      <TableCell>
                        <Select
                          value={mappedSectionNo !== undefined ? String(mappedSectionNo) : ""}
                          onChange={(e) => handleMapChange(cat.id, e.target.value)}
                          disabled={sections.length === 0}
                          aria-label={`${cat.name} için kısım seç`}
                          aria-invalid={mappedSectionNo === undefined}
                        >
                          <SelectItem value="">Kısım seçin</SelectItem>
                          {sections.map((s) => (
                            <SelectItem key={s.section_no} value={String(s.section_no)}>
                              {s.name} (KDV %{(s.tax_permyriad / 100).toFixed(0)})
                            </SelectItem>
                          ))}
                          {/* Keep an already-mapped-but-since-removed section selectable so the
                              admin can see and consciously change it, instead of it silently
                              disappearing from the dropdown. */}
                          {mappedSectionNo !== undefined && !mappedSection && (
                            <SelectItem value={String(mappedSectionNo)}>
                              Kısım #{mappedSectionNo} (cihazda bulunamadı)
                            </SelectItem>
                          )}
                        </Select>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <div className="flex justify-end">
        <Button onClick={handleSave} disabled={!branchId || !isDirty || replaceMappings.isPending}>
          {replaceMappings.isPending ? "Kaydediliyor..." : "Kaydet"}
        </Button>
      </div>
    </div>
  )
}
