"use client"

import { FileText, ListPlus, Plus } from "lucide-react"
import { useTranslations } from "next-intl"
import { useState } from "react"
import { toast } from "sonner"

import { MenuItemsDialog } from "@/components/catalog/menu-items-dialog"
import { FormDialog } from "@/components/layouts/form-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useMenus } from "@/hooks/use-catalog"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import api from "@/lib/api"
import type { Menu } from "@/types"

interface FormState {
  name: string
  description: string
  is_active: boolean
}

const defaultForm: FormState = { name: "", description: "", is_active: true }

function useCreateMenu() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Partial<Menu>) => api.post<Menu>("/api/v1/catalog/menus", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["menus"] })
    },
  })
}

export default function MenusPage() {
  const t = useTranslations("catalogMenus")
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState<FormState>(defaultForm)
  // The menu whose items are being edited. Holding the whole menu (not just an
  // id) keeps the sheet's title correct even while the list is refetching.
  const [itemsMenu, setItemsMenu] = useState<Menu | null>(null)

  const { data, isLoading } = useMenus()
  const createMenu = useCreateMenu()

  const menus = data ?? []

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim()) {
      toast.error(t("nameRequired"))
      return
    }
    try {
      await createMenu.mutateAsync({
        name: form.name.trim(),
        description: form.description.trim(),
        is_active: form.is_active,
      })
      toast.success(t("created"))
      setDialogOpen(false)
      setForm(defaultForm)
    } catch {
      toast.error(t("createFailed"))
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("title")}</h1>
          <p className="text-muted-foreground">{t("subtitle")}</p>
        </div>
        <Button onClick={() => { setForm(defaultForm); setDialogOpen(true) }}>
          <Plus className="size-4" />
          {t("add")}
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t("listTitle")}</CardTitle>
          <CardDescription>{t("listDescription")}</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}
            </div>
          ) : menus.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <FileText className="size-12 text-muted-foreground mb-4" />
              <h3 className="text-lg font-semibold">{t("emptyTitle")}</h3>
              <p className="text-sm text-muted-foreground mt-1 mb-4">{t("emptyDescription")}</p>
              <Button onClick={() => { setForm(defaultForm); setDialogOpen(true) }}>
                <Plus className="size-4" />
                {t("addFirst")}
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("columnName")}</TableHead>
                  <TableHead>{t("columnDescription")}</TableHead>
                  <TableHead>{t("columnStatus")}</TableHead>
                  <TableHead>{t("columnCreatedAt")}</TableHead>
                  <TableHead className="w-[160px]" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {menus.map((menu) => (
                  <TableRow key={menu.id}>
                    <TableCell className="font-medium">{menu.name}</TableCell>
                    <TableCell className="text-muted-foreground">{menu.description || "—"}</TableCell>
                    <TableCell>
                      <Badge
                        variant="outline"
                        className={
                          menu.is_active
                            ? "bg-status-success-bg text-status-success-fg border-status-success-border"
                            : "bg-status-neutral-bg text-status-neutral-fg border-status-neutral-border"
                        }
                      >
                        {menu.is_active ? t("statusActive") : t("statusPassive")}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {new Date(menu.created_at).toLocaleDateString("tr-TR")}
                    </TableCell>
                    <TableCell>
                      <Button variant="outline" size="sm" onClick={() => setItemsMenu(menu)}>
                        <ListPlus className="size-3.5" />
                        {t("manageItems")}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <FormDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        title={t("formTitle")}
        description={t("formDescription")}
        busy={createMenu.isPending}
        footer={
          <>
            <Button type="button" variant="outline" onClick={() => setDialogOpen(false)}>
              {t("cancel")}
            </Button>
            <Button type="submit" form="menu-form" disabled={createMenu.isPending}>
              {createMenu.isPending ? t("saving") : t("save")}
            </Button>
          </>
        }
      >
        <form id="menu-form" onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="menu-name">{t("name")}</Label>
            <Input
              id="menu-name"
              placeholder={t("namePlaceholder")}
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="menu-desc">{t("description")}</Label>
            <Input
              id="menu-desc"
              placeholder={t("descriptionPlaceholder")}
              value={form.description}
              onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
            />
          </div>
          <div className="flex items-center gap-3">
            <Switch
              id="menu-active"
              checked={form.is_active}
              onCheckedChange={(checked) => setForm((f) => ({ ...f, is_active: checked }))}
            />
            <Label htmlFor="menu-active">{t("active")}</Label>
          </div>
        </form>
      </FormDialog>

      {itemsMenu && (
        // Keyed per menu so the add form's local state does not leak from one
        // menu into the next.
        <MenuItemsDialog
          key={itemsMenu.id}
          open
          onOpenChange={(next) => {
            if (!next) setItemsMenu(null)
          }}
          menu={itemsMenu}
        />
      )}
    </div>
  )
}
