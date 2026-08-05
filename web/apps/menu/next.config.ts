import type { NextConfig } from "next"
import createNextIntlPlugin from "next-intl/plugin"

const withNextIntl = createNextIntlPlugin("./src/i18n/request.ts")

// Server-side origin of the Go API. Never shipped to the browser (no
// NEXT_PUBLIC_ prefix).
//
// CAUTION — this value is read at two different TIMES:
//   * the rewrite below is serialized into routes-manifest.json at BUILD time,
//     so changing PUBLIC_API_ORIGIN in a prebuilt image does nothing (verified:
//     a runtime-only value left the proxy pointing at the build-time default
//     and every proxied call answered 500 / ECONNREFUSED);
//   * the /q/[token] route handler reads it at RUNTIME, like normal server code.
// Keep them consistent by setting it for the build too, or let the edge proxy
// own the routing (see the rewrite comment).
const apiOrigin = process.env.PUBLIC_API_ORIGIN ?? "http://localhost:8081"

const nextConfig: NextConfig = {
  output: "standalone",

  // Same-origin proxy — this is the DEFAULT topology, not a dev convenience:
  //
  //   1. The guest cookie is scoped `Path=/api/public/v1` (backend
  //      storefront/http/guest_middleware.go). Proxying the identical path
  //      keeps that scope meaningful.
  //   2. The backend only emits CORS headers when APP_ENV=dev
  //      (backend/cmd/api/main.go devCORSMiddleware, "never in production").
  //      A cross-origin browser XHR from menu.example to api.example would
  //      therefore fail in production regardless of SameSite.
  //   3. A host-only Set-Cookie from the API reaches the browser bound to the
  //      menu origin, which is also the origin every later API call uses.
  //
  // Pointing NEXT_PUBLIC_PUBLIC_API_URL at an absolute URL bypasses this and
  // requires production CORS on the backend — not implemented today.
  //
  // In production the same-origin property is expected to come from the edge
  // proxy that deploy/docker-compose.prod.yml already assumes (Caddy/nginx/
  // Traefik, see its Keycloak notes): route /api/public/v1/* to the api
  // container and everything else to this app. This rewrite is then never
  // exercised, and is the dev-time stand-in for that edge rule.
  async rewrites() {
    return [
      {
        source: "/api/public/v1/:path*",
        destination: `${apiOrigin}/api/public/v1/:path*`,
      },
    ]
  },

  async headers() {
    return [
      {
        // no-referrer applies to the WHOLE app, not just /q/:token.
        //
        // A per-route override was tried first and does not work: a later
        // matching rule wins, so the broad rule silently replaced the narrow
        // one and /q/{token} was served with the weaker policy (observed, not
        // assumed). Since a /q URL carries a credential and nothing in this
        // app links out, the strict policy is simply the app-wide default —
        // one rule that cannot be overridden by accident.
        source: "/:path*",
        headers: [
          { key: "Referrer-Policy", value: "no-referrer" },
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "X-Robots-Tag", value: "noindex, nofollow" },
        ],
      },
      {
        source: "/q/:token",
        headers: [{ key: "Cache-Control", value: "no-store" }],
      },
    ]
  },
}

export default withNextIntl(nextConfig)
