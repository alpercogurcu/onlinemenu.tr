import type { NextConfig } from "next"
import createNextIntlPlugin from "next-intl/plugin"

const withNextIntl = createNextIntlPlugin("./src/i18n/request.ts")

const nextConfig: NextConfig = {
  output: "standalone",

  async rewrites() {
    const apiProxyEnabled = process.env.NEXT_PUBLIC_API_PROXY_ENABLED === "true"
    if (!apiProxyEnabled) return []

    // API_CORE_ORIGIN is server-only (no NEXT_PUBLIC_ prefix) and takes
    // precedence, mirroring PUBLIC_API_ORIGIN in apps/menu.
    //
    // It exists because NEXT_PUBLIC_API_CORE_URL carries two incompatible
    // meanings at once:
    //   * here and in src/app/api/pos/kitchen-stream/route.ts it must be the
    //     API's server-reachable origin (in the prod compose network,
    //     http://api:8080);
    //   * in src/lib/api.ts and src/lib/identity-bootstrap.ts it is the
    //     BROWSER's base URL, which must stay the same-origin "/api/core"
    //     path — the backend only emits CORS headers when APP_ENV=dev, so an
    //     absolute cross-origin value fails in production.
    // Setting the single shared variable therefore breaks one side or the
    // other. This override lets the container fix the rewrite without
    // assigning the browser an origin it cannot use.
    //
    // Left unset (dev, and every existing checkout) behaviour is unchanged.
    // NOTE: kitchen-stream/route.ts still reads only NEXT_PUBLIC_API_CORE_URL
    // and so still falls back to localhost:8081 inside a container.
    const apiCoreUrl =
      process.env.API_CORE_ORIGIN ??
      process.env.NEXT_PUBLIC_API_CORE_URL ??
      "http://localhost:8081"

    return [
      {
        // Strip /api/core prefix and forward to backend root
        source: "/api/core/:path*",
        destination: `${apiCoreUrl}/:path*`,
      },
    ]
  },

  webpack(config) {
    config.module.rules.push({
      test: /\.svg$/,
      use: ["@svgr/webpack"],
    })
    return config
  },
}

export default withNextIntl(nextConfig)
