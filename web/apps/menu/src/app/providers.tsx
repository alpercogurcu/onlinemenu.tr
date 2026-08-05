"use client"

import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { useState, type ReactNode } from "react"

export function Providers({ children }: { children: ReactNode }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            // A diner's phone locks and wakes constantly; refetching the whole
            // menu on every focus event would spend the session's rate-limit
            // budget on data that changes hourly at most. Per-query staleTime
            // (see use-storefront.ts) decides when it is actually worth it.
            refetchOnWindowFocus: false,
            retry: 1,
          },
        },
      }),
  )

  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}
