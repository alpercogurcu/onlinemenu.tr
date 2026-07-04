// Smoke test — vitest + @testing-library/react + jsdom harness'inin gerçekten
// çalıştığını doğrular. Kapsamlı component testleri değil; amaç yalnızca
// test altyapısının (config, setup, DOM ortamı) yeşil olduğunu kanıtlamak.
import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { Button } from "@/components/ui/button"

describe("Button (smoke)", () => {
  it("renders its children", () => {
    render(<Button>Kaydet</Button>)

    expect(screen.getByRole("button", { name: "Kaydet" })).toBeInTheDocument()
  })

  it("calls onClick when clicked", () => {
    const onClick = vi.fn()
    render(<Button onClick={onClick}>Kaydet</Button>)

    fireEvent.click(screen.getByRole("button", { name: "Kaydet" }))

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it("respects the disabled prop", () => {
    render(<Button disabled>Kaydet</Button>)

    expect(screen.getByRole("button", { name: "Kaydet" })).toBeDisabled()
  })
})
