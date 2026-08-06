import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import api from "@/lib/api"
import type { Category, Menu, MenuItem, ModifierGroup, Product } from "@/types"

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

export function useUpdateProduct() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }: Partial<Product> & { id: string }) =>
      api.put<Product>(`/api/v1/catalog/products/${id}`, body),
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
