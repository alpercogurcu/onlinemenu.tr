// Refuses the whole run unless the operator opted in explicitly. This is the
// guard that cannot be bypassed by forgetting an import in a spec.
export default function globalSetup(): void {
  if (process.env.E2E_PROD !== "1") {
    throw new Error(
      "e2e-prod: canlı prod ortamına yazan pakettir. E2E_PROD=1 verilmeden çalışmaz.\n" +
        "  set -a; . deploy/.env.diverserver.local; set +a\n" +
        "  E2E_PROD=1 playwright test -c e2e-prod/playwright.config.ts",
    )
  }
  for (const key of ["E2E_PROD_CLIENT_SECRET", "TEST_YONETICI_EMAIL", "TEST_YONETICI_PASSWORD"]) {
    if (!process.env[key]) throw new Error(`e2e-prod: ${key} ortam değişkeni eksik`)
  }
}
