import type { Metadata, Viewport } from "next"
import { NextIntlClientProvider } from "next-intl"
import { getLocale, getMessages, getTranslations } from "next-intl/server"

import { Geist } from "next/font/google"

import { Providers } from "./providers"

import "./globals.css"

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin", "latin-ext"],
  display: "swap",
})

export async function generateMetadata(): Promise<Metadata> {
  const t = await getTranslations("app")
  return {
    title: { template: t("titleTemplate"), default: t("title") },
    description: t("description"),
    // Nothing here is public content: every page needs a session cookie, and
    // /q/{token} URLs carry a credential. An indexed storefront URL would be a
    // leaked table token, not traffic.
    robots: { index: false, follow: false },
  }
}

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  // Zoom stays available — pinch-to-zoom is an accessibility affordance, and
  // disabling it on a menu with small prices is a WCAG 1.4.4 failure.
  maximumScale: 5,
  themeColor: "#ffffff",
}

export default async function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const locale = await getLocale()
  const messages = await getMessages()

  return (
    <html lang={locale}>
      <body className={`${geistSans.variable} font-sans`}>
        <NextIntlClientProvider messages={messages}>
          <Providers>{children}</Providers>
        </NextIntlClientProvider>
      </body>
    </html>
  )
}
