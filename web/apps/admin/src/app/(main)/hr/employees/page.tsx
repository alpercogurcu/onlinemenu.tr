"use client"

import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useEmployees } from "@/hooks/use-employees"
import type { EmployeeStatus, EmploymentType } from "@/types"

const statusConfig: Record<
  EmployeeStatus,
  { label: string; className: string }
> = {
  active: {
    label: "Aktif",
    className: "bg-status-success-bg text-status-success-fg border-status-success-border",
  },
  on_leave: {
    label: "İzinli",
    className: "bg-status-warning-bg text-status-warning-fg border-status-warning-border",
  },
  terminated: {
    label: "Ayrıldı",
    className: "bg-status-danger-bg text-status-danger-fg border-status-danger-border",
  },
}

const employmentTypeLabels: Record<EmploymentType, string> = {
  full_time: "Tam zamanlı",
  part_time: "Yarı zamanlı",
  seasonal: "Mevsimlik",
  contractor: "Sözleşmeli",
}

function formatDate(dateStr: string): string {
  return new Date(dateStr).toLocaleDateString("tr-TR", {
    year: "numeric",
    month: "short",
    day: "numeric",
  })
}

export default function EmployeesPage() {
  const { data, isLoading } = useEmployees()
  const employees = data ?? []

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Çalışanlar</h1>
        <p className="text-muted-foreground">
          Personel kayıtlarını görüntüleyin.
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Personel Listesi</CardTitle>
          <CardDescription>Tüm çalışanlarınız.</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-3">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-12 w-full" />
              ))}
            </div>
          ) : employees.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <p className="text-muted-foreground">Henüz çalışan kaydı yok</p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Kimlik</TableHead>
                  <TableHead>Departman</TableHead>
                  <TableHead>Unvan</TableHead>
                  <TableHead>Çalışma Tipi</TableHead>
                  <TableHead>Durum</TableHead>
                  <TableHead>İşe Giriş</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {employees.map((emp) => {
                  const status = statusConfig[emp.status]
                  return (
                    <TableRow key={emp.id}>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {emp.person_id}
                      </TableCell>
                      <TableCell>{emp.department || "—"}</TableCell>
                      <TableCell>{emp.job_title || "—"}</TableCell>
                      <TableCell>
                        {employmentTypeLabels[emp.employment_type]}
                      </TableCell>
                      <TableCell>
                        <Badge
                          variant="outline"
                          className={status.className}
                        >
                          {status.label}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {formatDate(emp.hire_date)}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
