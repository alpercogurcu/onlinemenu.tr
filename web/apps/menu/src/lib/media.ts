// The menu API returns `image_key`, an object-storage key — not a URL. No
// signed-URL or CDN signer exists anywhere in the platform yet, so the only
// honest thing to do is compose a plain URL against a configured public base
// and fall back to a placeholder when there is none.
const MEDIA_BASE_URL = process.env.NEXT_PUBLIC_MEDIA_BASE_URL ?? ""

export function imageUrl(imageKey: string): string | null {
  if (imageKey === "" || MEDIA_BASE_URL === "") return null
  const base = MEDIA_BASE_URL.replace(/\/+$/, "")
  const key = imageKey.replace(/^\/+/, "")
  return `${base}/${key}`
}
