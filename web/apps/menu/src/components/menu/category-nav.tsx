"use client"

import { useEffect, useRef, useState } from "react"
import { useTranslations } from "next-intl"

import { cn } from "@onlinemenu/ui-kit"

export interface CategoryNavItem {
  id: string
  title: string
}

export function sectionDomId(categoryId: string): string {
  return `category-section-${categoryId}`
}

// react-hooks/purity, bileşen gövdesindeki fonksiyonlarda Date.now'u render
// saflığı adına reddediyor; çağrı olay anında koştuğu için modül seviyesine
// taşımak hem kuralı hem niyeti karşılar.
function nowMs(): number {
  return Date.now()
}

function prefersReducedMotion(): boolean {
  return (
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  )
}

/**
 * Sticky category jump bar for the guest menu.
 *
 * Uzun bir menüde misafirin tek gezinme aracı parmakla kaydırmaktı; bu çubuk
 * kategoriye tek dokunuşla atlamayı sağlar. Header sticky (top-0, h-14)
 * olduğundan çubuk top-14'e yapışır; bölümler scroll-mt ile ikisinin altından
 * kurtulur (menu-screen.tsx).
 */
export function CategoryNav({ categories }: { categories: CategoryNavItem[] }) {
  const t = useTranslations("menu")
  const [activeId, setActiveId] = useState(categories[0]?.id ?? "")
  const chipRefs = useRef<Map<string, HTMLButtonElement>>(new Map())
  // Dokunuşla tetiklenen kaydırma sırasında gözlemci ara bölümleri "aktif"
  // sanıp çubuğu titretir; kayma bitene dek gözlemci güncellemesi bastırılır.
  const suppressUntil = useRef(0)

  useEffect(() => {
    if (typeof IntersectionObserver === "undefined") return
    const sections = categories
      .map((c) => document.getElementById(sectionDomId(c.id)))
      .filter((el): el is HTMLElement => el !== null)
    if (sections.length === 0) return

    // Header (56px) + çubuk (~48px) altındaki ilk görünür bölüm aktiftir.
    const observer = new IntersectionObserver(
      (entries) => {
        if (Date.now() < suppressUntil.current) return
        const visible = entries
          .filter((e) => e.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
        const top = visible[0]?.target.id
        if (top) setActiveId(top.replace("category-section-", ""))
      },
      { rootMargin: "-112px 0px -55% 0px" },
    )
    for (const section of sections) observer.observe(section)
    return () => observer.disconnect()
  }, [categories])

  useEffect(() => {
    // Aktif çip çubuğun görünür alanında kalmalı; inline:"nearest" sayfayı
    // dikeyde oynatmadan yalnız çubuğu yatayda kaydırır. jsdom scrollIntoView
    // sağlamaz, bu yüzden varlığı denetlenir.
    const chip = chipRefs.current.get(activeId)
    if (typeof chip?.scrollIntoView === "function") {
      chip.scrollIntoView({ block: "nearest", inline: "nearest" })
    }
  }, [activeId])

  function jumpTo(id: string) {
    const section = document.getElementById(sectionDomId(id))
    if (section === null) return
    setActiveId(id)
    suppressUntil.current = nowMs() + 800
    section.scrollIntoView({ behavior: prefersReducedMotion() ? "auto" : "smooth", block: "start" })
  }

  return (
    <nav
      aria-label={t("categoryJump")}
      className="bg-background/95 sticky top-14 z-20 -mx-4 border-b backdrop-blur"
    >
      <div className="flex gap-2 overflow-x-auto px-4 py-2 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
        {categories.map((category) => {
          const isActive = category.id === activeId
          return (
            <button
              key={category.id}
              type="button"
              ref={(el) => {
                if (el === null) chipRefs.current.delete(category.id)
                else chipRefs.current.set(category.id, el)
              }}
              aria-current={isActive ? "true" : undefined}
              onClick={() => jumpTo(category.id)}
              className={cn(
                "min-h-11 shrink-0 whitespace-nowrap rounded-full border px-4 text-sm font-medium transition-colors",
                isActive
                  ? "border-primary bg-primary text-primary-foreground"
                  : "border-border bg-card text-foreground active:bg-accent",
              )}
            >
              {category.title}
            </button>
          )
        })}
      </div>
    </nav>
  )
}
