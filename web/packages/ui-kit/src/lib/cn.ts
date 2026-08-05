import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

// Tailwind class merger shared by every ui-kit primitive. Kept here (and not
// re-exported from an app) so a component moved into this package never has a
// dangling `@/lib/utils` import back into an application.
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
