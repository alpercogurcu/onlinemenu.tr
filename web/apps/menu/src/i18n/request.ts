import { getRequestConfig } from "next-intl/server"

// Single locale today. The storefront is Turkey-only in the MVP; the plumbing
// stays so a second locale is a message file, not a refactor.
export const DEFAULT_LOCALE = "tr"

export default getRequestConfig(async () => ({
  locale: DEFAULT_LOCALE,
  messages: (await import(`../messages/${DEFAULT_LOCALE}.json`)).default,
}))
