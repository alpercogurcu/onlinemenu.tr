import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import api from "@/lib/api"
import type { Employee } from "@/types"

interface EmployeeListResponse {
  employees: Employee[]
}

export function useEmployees(params?: { status?: string; limit?: number }) {
  return useQuery({
    queryKey: ["employees", params],
    queryFn: async () => {
      const { data } = await api.get<EmployeeListResponse>("/api/v1/employees/", { params })
      return data?.employees ?? []
    },
  })
}

export function useEmployee(id: string) {
  return useQuery({
    queryKey: ["employees", id],
    queryFn: async () => {
      const { data } = await api.get<Employee>(`/api/v1/employees/${id}`)
      return data
    },
    enabled: Boolean(id),
  })
}

export function useCreateEmployee() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: Partial<Employee>) => api.post<Employee>("/api/v1/employees/", body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["employees"] })
    },
  })
}

export function useTerminateEmployee() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.post(`/api/v1/employees/${id}/terminate`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["employees"] })
    },
  })
}
