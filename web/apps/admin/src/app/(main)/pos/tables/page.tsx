import type { Metadata } from "next"

import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"

export const metadata: Metadata = {
  title: "Masalar",
}

const mockTables = [
  { id: 1, name: "Masa 1", status: "bos", capacity: 4 },
  { id: 2, name: "Masa 2", status: "dolu", capacity: 2 },
  { id: 3, name: "Masa 3", status: "dolu", capacity: 6 },
  { id: 4, name: "Masa 4", status: "bos", capacity: 4 },
  { id: 5, name: "Masa 5", status: "bos", capacity: 8 },
  { id: 6, name: "Masa 6", status: "dolu", capacity: 4 },
  { id: 7, name: "Masa 7", status: "bos", capacity: 2 },
  { id: 8, name: "Masa 8", status: "dolu", capacity: 6 },
  { id: 9, name: "Masa 9", status: "bos", capacity: 4 },
  { id: 10, name: "Bahçe 1", status: "bos", capacity: 8 },
  { id: 11, name: "Bahçe 2", status: "dolu", capacity: 6 },
  { id: 12, name: "VIP 1", status: "bos", capacity: 10 },
]

export default function TablesPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Masalar</h1>
        <p className="text-muted-foreground">
          Masa durumlarını gerçek zamanlı takip edin.
        </p>
      </div>

      <div className="flex items-center gap-4 text-sm">
        <div className="flex items-center gap-1.5">
          <div className="size-3 rounded-full bg-green-500" />
          <span className="text-muted-foreground">
            Boş ({mockTables.filter((t) => t.status === "bos").length})
          </span>
        </div>
        <div className="flex items-center gap-1.5">
          <div className="size-3 rounded-full bg-orange-500" />
          <span className="text-muted-foreground">
            Dolu ({mockTables.filter((t) => t.status === "dolu").length})
          </span>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
        {mockTables.map((table) => (
          <Card
            key={table.id}
            className="cursor-pointer hover:shadow-md transition-shadow py-0"
          >
            <CardHeader className="pb-2 pt-4 px-4">
              <CardTitle className="text-sm font-semibold">{table.name}</CardTitle>
            </CardHeader>
            <CardContent className="pb-4 px-4">
              <div className="flex flex-col gap-1.5">
                <Badge
                  className={
                    table.status === "bos"
                      ? "bg-green-100 text-green-700 border-green-200 hover:bg-green-100"
                      : "bg-orange-100 text-orange-700 border-orange-200 hover:bg-orange-100"
                  }
                  variant="outline"
                >
                  {table.status === "bos" ? "Boş" : "Dolu"}
                </Badge>
                <span className="text-xs text-muted-foreground">
                  {table.capacity} kişilik
                </span>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  )
}
