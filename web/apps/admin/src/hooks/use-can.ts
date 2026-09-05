import { can } from "@/lib/permissions"
import { useAuthStore } from "@/store/auth-store"

/**
 * Reactive wrapper around `can()` (see lib/permissions.ts for what this is
 * and, more importantly, what it is NOT). The CTX token itself lives outside
 * React state (module-level variable in lib/api.ts), so this hook
 * subscribes to `user` — set/cleared together with the token by
 * setSession/logout in store/auth-store.ts — purely to force a re-render
 * when the session (and therefore the token this reads) changes.
 */
export function useCan(action: string): boolean {
  const user = useAuthStore((s) => s.user)
  return user !== null && can(action)
}
