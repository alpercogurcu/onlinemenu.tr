import { useEffect, useState } from 'react'
import { ListProductModifierGroups, ListProducts } from '../../wailsjs/go/main/App'
import { main } from '../../wailsjs/go/models'
import type { LineOptions } from '../lib/cart'
import { formatMoney } from '../lib/format'
import { ErrorBanner } from './ErrorBanner'
import { SlidersIcon, TriangleAlertIcon } from './icons'
import { OptionPicker } from './OptionPicker'

type ProductGridProps = {
  categories: main.CategoryDTO[]
  disabled: boolean
  onAddProduct: (product: main.ProductDTO, options?: LineOptions) => void
}

/**
 * Middle column: fixed-order category tabs + product tile grid. Tile order
 * follows the category's product order (no client-side sort) so the
 * cashier builds muscle memory for tile position — per the design plan
 * ("SABİT sıralı tile'lar — hafıza kası").
 *
 * A product without options is added with one tap. A product with option
 * groups opens the OptionPicker instead (the tile carries a slider badge so the
 * cashier knows before tapping). If a product's options could not be fetched it
 * is still added immediately — selling never waits on the catalog — and the
 * receipt line warns; the grid then quietly re-fetches that product's options
 * so the next tap can offer them.
 */
export function ProductGrid({ categories, disabled, onAddProduct }: ProductGridProps) {
  const [activeCategoryId, setActiveCategoryId] = useState<string | null>(null)
  const [products, setProducts] = useState<main.ProductDTO[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [pickerProduct, setPickerProduct] = useState<main.ProductDTO | null>(null)

  useEffect(() => {
    if (categories.length > 0 && !activeCategoryId) {
      setActiveCategoryId(categories[0].id)
    }
  }, [categories, activeCategoryId])

  useEffect(() => {
    if (!activeCategoryId) return
    setLoading(true)
    setError('')
    ListProducts(activeCategoryId)
      .then(setProducts)
      .catch((err) => setError(String(err)))
      .finally(() => setLoading(false))
  }, [activeCategoryId])

  function refreshOptions(product: main.ProductDTO) {
    ListProductModifierGroups(product.id)
      .then((groups) => {
        setProducts((current) =>
          current.map((p) =>
            p.id === product.id ? main.ProductDTO.createFrom({ ...p, modifier_groups: groups, options_unavailable: false }) : p,
          ),
        )
      })
      // Best-effort self-healing only: the sale already went ahead without
      // options and the line carries the warning, so a second failure changes
      // nothing the cashier can act on.
      .catch(() => undefined)
  }

  function handleTileTap(product: main.ProductDTO) {
    if (product.modifier_groups.length > 0) {
      setPickerProduct(product)
      return
    }
    if (product.options_unavailable) refreshOptions(product)
    onAddProduct(product)
  }

  return (
    <section className="flex h-full flex-1 flex-col overflow-hidden bg-surface">
      <nav className="flex shrink-0 gap-1 overflow-x-auto border-b border-line px-3 py-2">
        {categories.map((cat) => (
          <button
            key={cat.id}
            type="button"
            onClick={() => setActiveCategoryId(cat.id)}
            className={`min-h-14 shrink-0 whitespace-nowrap rounded-md px-4 text-sm font-medium ${
              activeCategoryId === cat.id ? 'bg-panel text-ink' : 'text-ink-dim'
            }`}
          >
            {cat.name}
          </button>
        ))}
      </nav>

      <div className="flex-1 overflow-y-auto p-3">
        {loading && <p className="text-ink-dim">Ürünler yükleniyor…</p>}
        <ErrorBanner message={error} />
        {!loading && !error && products.length === 0 && (
          <p className="text-ink-dim">Bu kategoride ürün yok.</p>
        )}
        <div className="grid grid-cols-4 gap-2">
          {products.map((product) => (
            <button
              key={product.id}
              type="button"
              disabled={disabled}
              onClick={() => handleTileTap(product)}
              className="relative flex min-h-14 flex-col justify-between rounded-lg border border-line bg-panel p-3 text-left disabled:cursor-not-allowed disabled:opacity-40"
            >
              {product.modifier_groups.length > 0 && (
                <span className="absolute right-2 top-2 text-ink-dim" title="Seçenekli ürün">
                  <SlidersIcon size={16} />
                  <span className="sr-only">Seçenekli ürün</span>
                </span>
              )}
              {product.options_unavailable && (
                <span className="absolute right-2 top-2 text-warn" title="Seçenekler alınamadı">
                  <TriangleAlertIcon size={16} />
                  <span className="sr-only">Seçenekler alınamadı</span>
                </span>
              )}
              <span className="line-clamp-2 pr-6 text-sm font-medium text-ink">{product.name}</span>
              <span className="money text-sm font-semibold text-amber">
                {formatMoney(product.price_amount)}
              </span>
            </button>
          ))}
        </div>
        {disabled && (
          <p className="mt-3 text-sm text-ink-dim">
            Sipariş eklemek için önce bir adisyon seçin veya masa açın.
          </p>
        )}
      </div>

      {pickerProduct && (
        <OptionPicker
          product={pickerProduct}
          onCancel={() => setPickerProduct(null)}
          onConfirm={(options) => {
            onAddProduct(pickerProduct, options)
            setPickerProduct(null)
          }}
        />
      )}
    </section>
  )
}
