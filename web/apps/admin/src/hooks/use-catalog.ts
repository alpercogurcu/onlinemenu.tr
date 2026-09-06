import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query"

import api from "@/lib/api"
import type { Category, Menu, MenuItem, Modifier, ModifierGroup, Product, SelectionType } from "@/types"

export function useProducts(params?: { limit?: number; offset?: number }) {
  return useQuery({
    queryKey: ["products", params],
    queryFn: async () => {
      const { data } = await api.get<Product[]>("/api/v1/catalog/products", { params })
      return data ?? []
    },
  })
}

export function useProduct(id: string) {
  return useQuery({
    queryKey: ["products", id],
    queryFn: async () => {
      const { data } = await api.get<Product>(`/api/v1/catalog/products/${id}`)
      return data
    },
    enabled: Boolean(id),
  })
}

export function useCreateProduct() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Partial<Product>) => api.post<Product>("/api/v1/catalog/products", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["products"] })
    },
  })
}

// Backend PUT replaces the whole row from the body, so a partial call here
// would silently blank out whatever fields it omits (see C1 in the katalog-ux
// final review) — the variables type requires the full replace set so a
// missing field is a compile error, not a data-loss bug. Callers build it via
// toProductBody() in components/catalog/product-editor.tsx.
export function useUpdateProduct() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      id,
      ...body
    }: {
      id: string
      category_id: string | null
      name: string
      description: string
      price_amount: number
      currency: string
      unit: string
      tax_rate_bps: number
      is_active: boolean
      sort_order: number
      source_stock_item_id: string | null
    }) => api.put<Product>(`/api/v1/catalog/products/${id}`, body),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: ["products"] })
      void qc.invalidateQueries({ queryKey: ["products", variables.id] })
    },
  })
}

export function useDeleteProduct() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.delete(`/api/v1/catalog/products/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["products"] })
    },
  })
}

export function useCategories() {
  return useQuery({
    queryKey: ["categories"],
    queryFn: async () => {
      const { data } = await api.get<Category[]>("/api/v1/catalog/categories")
      return data ?? []
    },
  })
}

export function useCreateCategory() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Partial<Category>) => api.post<Category>("/api/v1/catalog/categories", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["categories"] })
    },
  })
}

export function useMenus() {
  return useQuery({
    queryKey: ["menus"],
    queryFn: async () => {
      const { data } = await api.get<Menu[]>("/api/v1/catalog/menus")
      return data ?? []
    },
  })
}

export function useMenuItems(menuId: string) {
  return useQuery({
    queryKey: ["menus", menuId, "items"],
    queryFn: async () => {
      const { data } = await api.get<MenuItem[]>(`/api/v1/catalog/menus/${menuId}/items`)
      return data ?? []
    },
    enabled: Boolean(menuId),
  })
}

// POST /menus/{id}/items is an upsert (ON CONFLICT (menu_id, product_id) DO
// UPDATE in MenuItemRepo.AddItem), so the same call both adds a product and
// edits an already-placed one's price override / active flag. It answers 204
// with no body — nothing to return to the caller.
export function useAddMenuItem() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      menuId,
      ...body
    }: {
      menuId: string
      product_id: string
      price_override: number | null
      is_active: boolean
      sort_order?: number
    }) => api.post(`/api/v1/catalog/menus/${menuId}/items`, body),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: ["menus", variables.menuId, "items"] })
    },
  })
}

export function useRemoveMenuItem() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ menuId, productId }: { menuId: string; productId: string }) =>
      api.delete(`/api/v1/catalog/menus/${menuId}/items/${productId}`),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: ["menus", variables.menuId, "items"] })
    },
  })
}

export function useModifierGroups() {
  return useQuery({
    queryKey: ["modifier-groups"],
    queryFn: async () => {
      const { data } = await api.get<ModifierGroup[]>("/api/v1/catalog/modifier-groups")
      return data ?? []
    },
  })
}

export function useModifierGroup(id: string) {
  return useQuery({
    queryKey: ["modifier-groups", id],
    queryFn: async () => {
      const { data } = await api.get<ModifierGroup>(`/api/v1/catalog/modifier-groups/${id}`)
      return data
    },
    enabled: Boolean(id),
  })
}

export function useCreateModifierGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Partial<ModifierGroup>) =>
      api.post<ModifierGroup>("/api/v1/catalog/modifier-groups", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["modifier-groups"] })
    },
  })
}

// Same reasoning as useUpdateProduct — full replace body required (see I1 in
// the katalog-ux final review, group sort_order silently reset to 0).
export function useUpdateModifierGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      id,
      ...body
    }: {
      id: string
      name: string
      selection_type: SelectionType
      min_selections: number
      max_selections: number | null
      is_required: boolean
      sort_order: number
    }) => api.put<ModifierGroup>(`/api/v1/catalog/modifier-groups/${id}`, body),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: ["modifier-groups"] })
      void qc.invalidateQueries({ queryKey: ["modifier-groups", variables.id] })
    },
  })
}

export function useDeleteModifierGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.delete(`/api/v1/catalog/modifier-groups/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["modifier-groups"] })
    },
  })
}

export function useModifiers(groupId: string) {
  return useQuery({
    queryKey: ["modifiers", groupId],
    queryFn: async () => {
      const { data } = await api.get<Modifier[]>(`/api/v1/catalog/modifier-groups/${groupId}/modifiers`)
      return data ?? []
    },
    enabled: Boolean(groupId),
  })
}

export function useCreateModifier() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      groupId,
      ...body
    }: {
      groupId: string
      name: string
      price_delta: number
      is_active: boolean
      sort_order?: number
    }) => api.post<Modifier>(`/api/v1/catalog/modifier-groups/${groupId}/modifiers`, body),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: ["modifiers", variables.groupId] })
    },
  })
}

// Backend PUT replaces the whole row from the body, so a partial call here
// would silently blank out whatever fields it omits — the variables type
// requires all four so a partial patch is a compile error, not a runtime bug.
// Callers (modifier-options-editor.tsx) build the full body via
// buildFullBody() before calling this.
export function useUpdateModifier() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      groupId,
      id,
      ...body
    }: {
      groupId: string
      id: string
      name: string
      price_delta: number
      is_active: boolean
      sort_order: number
    }) => api.put<Modifier>(`/api/v1/catalog/modifier-groups/${groupId}/modifiers/${id}`, body),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: ["modifiers", variables.groupId] })
    },
  })
}

export function useDeleteModifier() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ groupId, id }: { groupId: string; id: string }) =>
      api.delete(`/api/v1/catalog/modifier-groups/${groupId}/modifiers/${id}`),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: ["modifiers", variables.groupId] })
    },
  })
}

// A product's assigned modifier group ids — GET /products/{id}/modifier-groups.
export function useProductModifierGroupIds(productId: string) {
  return useQuery({
    queryKey: ["product-modifier-groups", productId],
    queryFn: async () => {
      const { data } = await api.get<string[]>(`/api/v1/catalog/products/${productId}/modifier-groups`)
      return data ?? []
    },
    enabled: Boolean(productId),
  })
}

// Batched version of useProductModifierGroupIds for a list page: one request
// per product (useQueries, not a single joined call the backend does not
// support), folded into a plain productId -> groupIds map so a table row can
// read its own entry without re-deriving anything. Shares the exact same
// query key as useProductModifierGroupIds, so both hooks read/write the same
// cache entry per product.
export function useProductsModifierGroupIds(productIds: string[]): Record<string, string[]> {
  const results = useQueries({
    queries: productIds.map((productId) => ({
      queryKey: ["product-modifier-groups", productId] as const,
      queryFn: async () => {
        const { data } = await api.get<string[]>(`/api/v1/catalog/products/${productId}/modifier-groups`)
        return data ?? []
      },
      enabled: Boolean(productId),
    })),
  })

  const map: Record<string, string[]> = {}
  productIds.forEach((productId, i) => {
    map[productId] = results[i]?.data ?? []
  })
  return map
}

// Assigning/removing a group on a product invalidates BOTH sides of the
// relationship: the product's own group list (product detail page) and the
// group's product list (modifier group detail page's "used by" card) — see
// query key sözleşmesi in the task brief.
export function useAssignModifierGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({
      productId,
      groupId,
      sortOrder,
    }: {
      productId: string
      groupId: string
      sortOrder?: number
    }) =>
      api.post(`/api/v1/catalog/products/${productId}/modifier-groups`, {
        group_id: groupId,
        sort_order: sortOrder ?? 0,
      }),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: ["product-modifier-groups", variables.productId] })
      void qc.invalidateQueries({ queryKey: ["group-products", variables.groupId] })
    },
  })
}

export function useRemoveModifierGroup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ productId, groupId }: { productId: string; groupId: string }) =>
      api.delete(`/api/v1/catalog/products/${productId}/modifier-groups/${groupId}`),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: ["product-modifier-groups", variables.productId] })
      void qc.invalidateQueries({ queryKey: ["group-products", variables.groupId] })
    },
  })
}

// Products currently using a modifier group — GET
// /modifier-groups/{id}/products (T1 ucu). Reverse of
// useProductModifierGroupIds, used by the group detail page's "used by" card
// and (batched via useQueries with the same query key) the groups list page.
export function useGroupProductIds(groupId: string) {
  return useQuery({
    queryKey: ["group-products", groupId],
    queryFn: async () => {
      const { data } = await api.get<string[]>(`/api/v1/catalog/modifier-groups/${groupId}/products`)
      return data ?? []
    },
    enabled: Boolean(groupId),
  })
}
