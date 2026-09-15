// Pure derivation of the kitchen-stream bridge route's upstream WebSocket
// origin (see route.ts's header comment for why the bridge exists at all).
// Split out of route.ts so the API_CORE_ORIGIN vs NEXT_PUBLIC_API_CORE_URL
// priority — the same two-incompatible-meanings problem next.config.ts's
// rewrite already solves for the HTTP proxy — can be unit-tested without
// touching the real `ws` socket or Next's request/response machinery.
//
// Callers must pass the two env values in explicitly (not a `process.env`
// object): Next statically inlines `process.env.NEXT_PUBLIC_*` literals at
// build time wherever that exact expression appears in source, so the read
// has to stay written as `process.env.NEXT_PUBLIC_API_CORE_URL` in route.ts
// for the build to bake in the right value. A property access through a
// passed-in object would not be recognised by that static replacement and
// would silently read `undefined` in the compiled server bundle.
export function backendWsOrigin(serverOrigin: string | undefined, publicUrl: string | undefined): string {
  const httpOrigin = serverOrigin ?? publicUrl ?? "http://localhost:8081"
  return httpOrigin.replace(/^http/, "ws").replace(/\/+$/, "")
}
