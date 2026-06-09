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
    className: "bg-green-100 text-green-700 border-green-200",
  },
  on_leave: {
    label: "İzinli",
    className: "bg-yellow-100 text-yellow-700 border-yellow-200",
  },
  terminated: {
    label: "Ayrıldı",
    className: "bg-red-100 text-red-700 border-red-200",
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
