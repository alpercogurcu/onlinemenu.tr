import { getRequestConfig } from "next-intl/server"

// Single locale today. The storefront is Turkey-only in the MVP; the plumbing
// stays so a second locale is a message file, not a refactor.
export const DEFAULT_LOCALE = "tr"

// Turkey-only storefront: pinning the zone keeps server and client renders of
// order timestamps identical (next-intl otherwise falls back to each
// environment's local zone and hydration diverges).
export const DEFAULT_TIME_ZONE = "Europe/Istanbul"

export default getRequestConfig(async () => ({
  locale: DEFAULT_LOCALE,
  timeZone: DEFAULT_TIME_ZONE,
  formats: {
    dateTime: {
      short: {
        day: "2-digit",
        month: "2-digit",
        year: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      },
    },
  },
  messages: (await import(`../messages/${DEFAULT_LOCALE}.json`)).default,
}))
