import { Suspense } from "react"

import AuthCallbackClient from "./callback-client"

// useSearchParams (read inside AuthCallbackClient) requires a Suspense
// boundary in the App Router, otherwise the page opts out of static
// rendering with a build warning.
export default function AuthCallbackPage() {
  return (
    <Suspense fallback={null}>
      <AuthCallbackClient />
    </Suspense>
  )
}
