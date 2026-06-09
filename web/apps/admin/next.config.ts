import type { NextConfig } from "next"
import createNextIntlPlugin from "next-intl/plugin"

const withNextIntl = createNextIntlPlugin("./src/i18n/request.ts")

const nextConfig: NextConfig = {
  output: "standalone",

  async rewrites() {
    const apiProxyEnabled = process.env.NEXT_PUBLIC_API_PROXY_ENABLED === "true"
    if (!apiProxyEnabled) return []

    const apiCoreUrl = process.env.NEXT_PUBLIC_API_CORE_URL ?? "http://localhost:8081"

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
