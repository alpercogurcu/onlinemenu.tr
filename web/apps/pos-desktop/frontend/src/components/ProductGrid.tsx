import { useEffect, useState } from 'react'
import { ListProducts } from '../../wailsjs/go/main/App'
import type { main } from '../../wailsjs/go/models'
import { formatMoney } from '../lib/format'
import { ErrorBanner } from './ErrorBanner'

type ProductGridProps = {
  categories: main.CategoryDTO[]
  disabled: boolean
  onAddProduct: (product: main.ProductDTO) => void
}

/**
 * Middle column: fixed-order category tabs + product tile grid. Tile order
 * follows the category's product order (no client-side sort) so the
 * cashier builds muscle memory for tile position — per the design plan
 * ("SABİT sıralı tile'lar — hafıza kası").
 */
export function ProductGrid({ categories, disabled, onAddProduct }: ProductGridProps) {
  const [activeCategoryId, setActiveCategoryId] = useState<string | null>(null)
  const [products, setProducts] = useState<main.ProductDTO[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

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
              onClick={() => onAddProduct(product)}
              className="flex min-h-14 flex-col justify-between rounded-lg border border-line bg-panel p-3 text-left disabled:cursor-not-allowed disabled:opacity-40"
            >
              <span className="line-clamp-2 text-sm font-medium text-ink">{product.name}</span>
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
    </section>
  )
}
