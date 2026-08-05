// Shared UI primitives — shadcn/ui (new-york) wrappers.
// Import: import { Button } from "@onlinemenu/ui-kit"
//
// The components here are copies of apps/admin's shadcn primitives, rewired to
// the local `cn` helper. Admin deliberately still uses its own copies (see
// docs/plans/2026-08-05-online-order-mvp.md WP4): migrating it is a separate,
// reviewable change, and apps may not import each other (ADR-ARCH-005).
//
// Consumers must make Tailwind scan this package's source — it resolves through
// node_modules via the pnpm workspace symlink, which Tailwind v4 skips by
// default. See apps/menu/src/app/globals.css `@source`.
export { cn } from "./lib/cn"

export { Badge, badgeVariants } from "./components/badge"
export { Button, buttonVariants } from "./components/button"
export {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "./components/card"
export {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "./components/sheet"
export { Skeleton } from "./components/skeleton"
