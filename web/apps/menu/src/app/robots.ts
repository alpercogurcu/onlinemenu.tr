import type { MetadataRoute } from "next"

// Nothing on this surface is public content. Every page needs a guest session
// cookie, and a /q/{token} URL IS a credential — an indexed one would be a
// table's ordering session handed to anyone who searches for it.
export default function robots(): MetadataRoute.Robots {
  return {
    rules: [{ userAgent: "*", disallow: "/" }],
  }
}
