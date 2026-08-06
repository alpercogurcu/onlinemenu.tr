# syntax=docker/dockerfile:1.7
# =============================================================================
# @onlinemenu/menu — guest QR storefront (Next.js 16, App Router).
#
# Build context is web/ (the pnpm workspace root), NOT apps/menu: the app
# depends on @onlinemenu/ui-kit and @onlinemenu/config via `workspace:*`, so a
# context rooted at the app cannot resolve them.
#
#   docker build -f deploy/menu/Dockerfile web
#
# The Go API is built by ko (no Dockerfile, see CLAUDE.md). This app is not:
# `next build` has no ko equivalent, so it gets a conventional multi-stage
# Dockerfile.
# =============================================================================

# node:22-alpine matches the CI runner's Node 22 and the `engines.node: >=22`
# constraint in web/package.json. The major.variant pin follows the same
# convention the rest of deploy/ uses for images (redis:8-alpine,
# postgres:17-alpine) — not a digest, but never "latest".
FROM node:22-alpine AS base
# corepack pins pnpm to the 9.x line CI installs (pnpm/action-setup version: 9).
# Building with a different pnpm major than CI resolves the lockfile with is how
# an image ends up with dependencies no CI job ever tested.
RUN corepack enable && corepack prepare pnpm@9.15.9 --activate
WORKDIR /repo

# --- deps -------------------------------------------------------------------
# Manifests only. This layer is invalidated by dependency changes, not by
# source changes, which is what keeps rebuilds off the network.
#
# Every workspace manifest is copied, not just the ones menu needs:
# --frozen-lockfile verifies the lockfile against the whole workspace, and a
# missing package.json makes that check fail rather than fall back.
FROM base AS deps
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/admin/package.json apps/admin/
COPY apps/menu/package.json apps/menu/
COPY apps/pos-desktop/frontend/package.json apps/pos-desktop/frontend/
COPY packages/config/package.json packages/config/
COPY packages/types/package.json packages/types/
COPY packages/ui-kit/package.json packages/ui-kit/
RUN --mount=type=cache,id=pnpm-store,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile --filter @onlinemenu/menu...

# --- build ------------------------------------------------------------------
FROM base AS build
COPY --from=deps /repo/node_modules ./node_modules
COPY --from=deps /repo/apps/menu/node_modules ./apps/menu/node_modules
COPY --from=deps /repo/packages ./packages
COPY . .

# PUBLIC_API_ORIGIN is a BUILD-time input, not only a runtime one.
#
# next.config.ts rewrites /api/public/v1/* to ${PUBLIC_API_ORIGIN}, and Next
# serialises rewrite destinations into routes-manifest.json during
# `next build`. Setting this variable only in the container's environment
# leaves the proxy pointing at the build-time default — the app's own config
# comment records that being observed as ECONNREFUSED on every proxied call.
#
# Consequence, stated plainly: the resulting image is environment-specific.
# It is not a portable artifact the way ${API_IMAGE} is. Rebuild it whenever
# the API's in-network address changes.
ARG PUBLIC_API_ORIGIN=http://api:8080
ENV PUBLIC_API_ORIGIN=${PUBLIC_API_ORIGIN}

# Public base for product images. NEXT_PUBLIC_* is inlined into the client
# bundle at build time, so this cannot be supplied at runtime either.
#
# Empty by default and left empty in the prod compose on purpose: the API
# returns an object-storage key, no signed-URL or CDN signer exists yet, and
# platform/storage has no concrete MinIO implementation (see the minio service
# note in docker-compose.prod.yml). src/lib/media.ts renders a placeholder
# while this is empty, which is the correct behaviour today. Populating it
# requires a rebuild.
ARG NEXT_PUBLIC_MEDIA_BASE_URL=
ENV NEXT_PUBLIC_MEDIA_BASE_URL=${NEXT_PUBLIC_MEDIA_BASE_URL}

# Deliberately NOT set: NEXT_PUBLIC_PUBLIC_API_URL. src/lib/api.ts uses an
# empty baseURL so every browser call is same-origin and goes through the
# rewrite above; an absolute value would need production CORS, which the
# backend does not implement.

ENV NEXT_TELEMETRY_DISABLED=1
ENV NODE_ENV=production
RUN pnpm --filter @onlinemenu/menu build

# --- runtime ----------------------------------------------------------------
# `output: "standalone"` (web/apps/menu/next.config.ts) emits a self-contained
# tree with only the traced subset of node_modules. pnpm's store, the other
# workspace apps and the whole toolchain stay in the builder stage.
FROM node:22-alpine AS runtime
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1

# HOSTNAME=0.0.0.0 is required, not cosmetic: Next's standalone server binds
# localhost by default, which is unreachable from outside the container.
ENV PORT=3001
ENV HOSTNAME=0.0.0.0

WORKDIR /app

# The node image already ships an unprivileged `node` user (uid 1000); a second
# one would add nothing. Ownership is set at COPY time so the process can write
# .next/cache (Next's fetch cache) without the image being world-writable.
#
# Standalone output for a monorepo is rooted at the WORKSPACE root, so the
# entrypoint lives at apps/menu/server.js, not server.js.
COPY --from=build --chown=node:node /repo/apps/menu/.next/standalone ./
COPY --from=build --chown=node:node /repo/apps/menu/.next/static ./apps/menu/.next/static
# No apps/menu/public: the app has no static asset directory today. A COPY of a
# non-existent path fails the build, so this is deliberately absent rather than
# defensively guarded.

USER node
EXPOSE 3001

# busybox wget (present in alpine) — "/" is a static 200 landing page telling a
# visitor to scan a QR code, so it exercises the Node server without touching
# the API, Redis or Postgres. A healthcheck that depended on the API would
# report this container unhealthy during an API restart.
HEALTHCHECK --interval=15s --timeout=5s --retries=5 --start-period=20s \
  CMD wget -q -O /dev/null http://127.0.0.1:3001/ || exit 1

CMD ["node", "apps/menu/server.js"]
