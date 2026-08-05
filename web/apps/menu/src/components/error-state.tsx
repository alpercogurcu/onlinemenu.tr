"use client"

import { AlertTriangleIcon } from "lucide-react"
import { useTranslations } from "next-intl"
import type { ReactNode } from "react"

import { Button } from "@onlinemenu/ui-kit"

import { API_ERROR_CODES, type ApiProblem } from "@/lib/api"

type ErrorMessageKey = keyof IntlMessages["errors"]

// Every code the API can send, mapped to a message key. The map is explicit
// rather than `t(problem.code)` so a code the backend adds later shows up as a
// missing entry here, not as a raw identifier printed on a diner's phone.
const CODE_MESSAGES: Record<string, ErrorMessageKey> = {
  [API_ERROR_CODES.notFound]: "not_found",
  [API_ERROR_CODES.unauthorized]: "unauthorized",
  [API_ERROR_CODES.invalidRequest]: "invalid_request",
  [API_ERROR_CODES.validationFailed]: "validation_failed",
  [API_ERROR_CODES.tableNotReady]: "table_not_ready",
  [API_ERROR_CODES.tableOccupied]: "table_occupied",
  [API_ERROR_CODES.tableBranchMismatch]: "table_branch_mismatch",
  [API_ERROR_CODES.internal]: "internal_error",
  [API_ERROR_CODES.rateLimited]: "rate_limited",
  [API_ERROR_CODES.unavailable]: "unavailable",
  [API_ERROR_CODES.network]: "network_error",
  [API_ERROR_CODES.unknown]: "unknown_error",
}

/**
 * Turns a problem into a diner-facing message.
 *
 * The server sends `detail` in Turkish and it is the better text when present,
 * but the CODE is what this app branches on: `detail` is explicitly documented
 * as changeable, and a client that keys on prose breaks on a copy edit.
 */
export function useProblemMessage() {
  const t = useTranslations("errors")

  return (problem: ApiProblem | null): string => {
    if (problem === null) return t("unknown_error")
    if (problem.detail !== "") return problem.detail
    return t(CODE_MESSAGES[problem.code] ?? "unknown_error")
  }
}

export function ErrorState({
  title,
  message,
  hint,
  action,
}: {
  title: string
  message: string
  hint?: string
  action?: ReactNode
}) {
  return (
    <div className="flex flex-col items-center gap-3 px-6 py-16 text-center">
      <AlertTriangleIcon className="text-destructive size-10" aria-hidden="true" />
      <h1 className="text-lg font-semibold">{title}</h1>
      <p className="text-muted-foreground text-sm">{message}</p>
      {hint === undefined ? null : <p className="text-muted-foreground text-sm">{hint}</p>}
      {action}
    </div>
  )
}

export function RetryButton({ onRetry, label }: { onRetry: () => void; label: string }) {
  return (
    <Button size="touch" variant="outline" onClick={onRetry}>
      {label}
    </Button>
  )
}
