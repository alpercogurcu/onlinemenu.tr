# syntax=docker/dockerfile:1.7
# =============================================================================
# @onlinemenu/admin — staff admin panel (Next.js 16, App Router).
#
# Same shape as deploy/menu/Dockerfile; read that one first for the workspace
# and standalone-output reasoning. Build context is web/:
#
#   docker build -f deploy/admin/Dockerfile web
#
# ⚠️ NEXT_PUBLIC_* variables are INLINED INTO THE CLIENT BUNDLE at build time.
# They are build args here, not runtime environment: passing them only in
# docker-compose `environment:` changes nothing the browser can see. Anything
# passed here is also publicly readable in the shipped JavaScript — never put a
# secret in one.
# =============================================================================

FROM node:22-alpine AS base
RUN corepack enable && corepack prepare pnpm@9.15.9 --activate
WORKDIR /repo

FROM base AS deps
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/admin/package.json apps/admin/
COPY apps/menu/package.json apps/menu/
COPY apps/pos-desktop/frontend/package.json apps/pos-desktop/frontend/
COPY packages/config/package.json packages/config/
COPY packages/types/package.json packages/types/
COPY packages/ui-kit/package.json packages/ui-kit/
RUN --mount=type=cache,id=pnpm-store,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile --filter @onlinemenu/admin...

FROM base AS build
COPY --from=deps /repo/node_modules ./node_modules
COPY --from=deps /repo/apps/admin/node_modules ./apps/admin/node_modules
COPY --from=deps /repo/packages ./packages
COPY . .

# Server-only. Rewrites are serialised at build time, so this must be present
# for the build; next.config.ts also documents why it is separate from
# NEXT_PUBLIC_API_CORE_URL (one variable, two incompatible meanings).
ARG API_CORE_ORIGIN=http://api:8080
ENV API_CORE_ORIGIN=${API_CORE_ORIGIN}

# Turns the /api/core/* rewrite on. Without it next.config.ts returns no
# rewrites and every browser call to /api/core/* 404s at this app.
ARG NEXT_PUBLIC_API_PROXY_ENABLED=true
ENV NEXT_PUBLIC_API_PROXY_ENABLED=${NEXT_PUBLIC_API_PROXY_ENABLED}

# Deliberately NOT set: src/lib/api.ts falls back to the relative "/api/core"
# only while NEXT_PUBLIC_API_CORE_URL is empty, and that relative path is what
# keeps the browser same-origin. Setting it would hand the browser an absolute
# origin the backend refuses (CORS is dev-only).

# The QR dialog builds guest menu links from this. It is read in a "use client"
# component, so it is baked into the bundle — a runtime-only value would leave
# every printed QR pointing at http://localhost:3001.
ARG NEXT_PUBLIC_MENU_URL=http://localhost:3001
ENV NEXT_PUBLIC_MENU_URL=${NEXT_PUBLIC_MENU_URL}

# Browser-side OIDC. Must be the PUBLIC Keycloak URL (the browser is
# redirected there), never http://keycloak:8080.
ARG NEXT_PUBLIC_KEYCLOAK_URL=http://localhost:8090
ENV NEXT_PUBLIC_KEYCLOAK_URL=${NEXT_PUBLIC_KEYCLOAK_URL}
ARG NEXT_PUBLIC_KEYCLOAK_REALM=onlinemenu
ENV NEXT_PUBLIC_KEYCLOAK_REALM=${NEXT_PUBLIC_KEYCLOAK_REALM}

# ⚠️ Fail-open upstream: src/app/(auth)/login/page.tsx enables the passwordless
# dev-login form unless this is exactly the string "false". The default is
# flipped to "false" HERE so a production image cannot inherit the dev
# shortcut by omission. Overriding it to anything else re-enables that form.
ARG NEXT_PUBLIC_ENABLE_DEV_LOGIN=false
ENV NEXT_PUBLIC_ENABLE_DEV_LOGIN=${NEXT_PUBLIC_ENABLE_DEV_LOGIN}

ENV NEXT_TELEMETRY_DISABLED=1
ENV NODE_ENV=production
RUN pnpm --filter @onlinemenu/admin build

FROM node:22-alpine AS runtime
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV PORT=3000
ENV HOSTNAME=0.0.0.0

WORKDIR /app

COPY --from=build --chown=node:node /repo/apps/admin/.next/standalone ./
COPY --from=build --chown=node:node /repo/apps/admin/.next/static ./apps/admin/.next/static
# No apps/admin/public directory exists — see the note in deploy/menu/Dockerfile.

USER node
EXPOSE 3000

# /login is a static, unauthenticated page that renders without contacting the
# API or Keycloak, so it proves the Node server is serving rather than proving
# the whole stack is up. "/" redirects into an auth-gated area and is a worse
# probe. --spider is avoided: it issues HEAD, which Next answers differently.
HEALTHCHECK --interval=15s --timeout=5s --retries=5 --start-period=30s \
  CMD wget -q -O /dev/null http://127.0.0.1:3000/login || exit 1

CMD ["node", "apps/admin/server.js"]
