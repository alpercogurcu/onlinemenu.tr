// Server-side WS→HTTP-stream bridge for the kitchen display (KDS).
//
// Why this file exists: the backend's kitchen WebSocket endpoint
// (backend/internal/modules/pos/ws/handler.go, KitchenWSPath) authenticates
// exclusively via an `Authorization: Bearer <token>` header
// (backend/internal/platform/auth/middleware.go, extractBearerToken) — there
// is no cookie, query-param or Sec-WebSocket-Protocol fallback, and the
// browser's native WebSocket API cannot set that header on the handshake
// request. A browser page can never open that socket directly.
//
// This Node route handler can: it runs server-side, so it has no header
// restriction. It opens the real WebSocket to the backend (with the header),
// and re-exposes the (receive-only, per KDS wire contract §5) frame stream
// to the browser as a newline-delimited JSON (NDJSON) HTTP response body,
// which `fetch()` + `Response.body` can read incrementally — `fetch()`,
// unlike `WebSocket`, CAN set arbitrary request headers from the browser.
//
// This keeps the exact same bearer-token auth model the rest of the admin
// app already uses (src/lib/api.ts) with no new token storage or exposure,
// and requires no backend change and no custom server (verified empirically
// that Next.js Node route handlers flush ReadableStream chunks incrementally
// under `next dev`, `next start`, and the actual `output: standalone`
// server.js used in production — see Sprint-4 Wave 2 report).
import { NextRequest } from "next/server"
import WebSocket from "ws"

export const runtime = "nodejs"
export const dynamic = "force-dynamic"

// Mirrors backend/internal/modules/pos/ws/handler.go's KitchenWSPath. Keep in
// sync with that file if the backend route ever moves.
const KITCHEN_WS_PATH = "/api/v1/pos/ws/kitchen"

// How long we wait for the upstream WebSocket handshake to finish before
// giving up and telling the browser to retry. Generous because the first
// connection after a cold backend start can be slow, but bounded so a dead
// backend doesn't leave the browser's fetch() hanging forever.
const CONNECT_TIMEOUT_MS = 10_000

function backendWsOrigin(): string {
  const httpOrigin = process.env.NEXT_PUBLIC_API_CORE_URL ?? "http://localhost:8081"
  return httpOrigin.replace(/^http/, "ws").replace(/\/+$/, "")
}

function toText(data: WebSocket.RawData): string {
  if (Array.isArray(data)) return Buffer.concat(data).toString("utf8")
  if (data instanceof ArrayBuffer) return Buffer.from(data).toString("utf8")
  return data.toString("utf8")
}

export async function GET(request: NextRequest) {
  const authHeader = request.headers.get("authorization")
  if (!authHeader) {
    return new Response("unauthorized", { status: 401 })
  }

  const branchId = request.nextUrl.searchParams.get("branch_id")
  if (!branchId) {
    return new Response("branch_id is required", { status: 422 })
  }

  const targetUrl = `${backendWsOrigin()}${KITCHEN_WS_PATH}?branch_id=${encodeURIComponent(branchId)}`
  const socket = new WebSocket(targetUrl, {
    headers: { Authorization: authHeader },
    handshakeTimeout: CONNECT_TIMEOUT_MS,
  })

  // Messages that arrive between `open` firing and the ReadableStream's
  // start() attaching its own listener must not be dropped — buffer them.
  const backlog: string[] = []
  let buffering = true
  socket.on("message", (data) => {
    if (buffering) backlog.push(toText(data))
  })

  const handshake = await new Promise<{ ok: true } | { ok: false; status: number }>((resolve) => {
    socket.once("open", () => resolve({ ok: true }))
    socket.once("unexpected-response", (_req, res) => {
      resolve({ ok: false, status: res.statusCode || 502 })
    })
    socket.once("error", () => resolve({ ok: false, status: 502 }))
  })

  if (!handshake.ok) {
    socket.terminate()
    return new Response("upstream kitchen socket unavailable", { status: handshake.status })
  }

  if (request.signal.aborted) {
    socket.terminate()
    return new Response(null, { status: 499 })
  }
  request.signal.addEventListener("abort", () => socket.terminate())

  const encoder = new TextEncoder()
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      buffering = false
      for (const line of backlog) controller.enqueue(encoder.encode(line + "\n"))
      backlog.length = 0

      socket.on("message", (data) => {
        try {
          controller.enqueue(encoder.encode(toText(data) + "\n"))
        } catch {
          // controller already closed (client disconnected) — swallow, the
          // "close" listener below has already unwound the socket.
        }
      })
      socket.on("close", () => {
        try {
          controller.close()
        } catch {
          // already closed
        }
      })
      socket.on("error", (err) => {
        try {
          controller.error(err)
        } catch {
          // already closed
        }
      })
    },
    cancel() {
      socket.terminate()
    },
  })

  return new Response(stream, {
    status: 200,
    headers: {
      "Content-Type": "application/x-ndjson; charset=utf-8",
      "Cache-Control": "no-store",
      "X-Accel-Buffering": "no",
    },
  })
}
