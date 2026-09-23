// @onlinemenu/pos-core — order-taking rules shared across every client that
// lets a cashier/waiter build a cart and take a payment: today pos-desktop
// (Wails), soon the admin garson (waiter) order screen (Next.js).
//
// Everything here is plain, framework-free TypeScript with no React, no Wails
// bindings and no DOM: cart/line building (cart.ts), modifier-group rules
// (options.ts), numeric-entry state (numpad.ts), money formatting/arithmetic
// (format.ts, payment.ts), payment-plan/split-payment rules (paymentPlan.ts,
// paymentLines.ts), table-target rules for taşı/birleştir (checkActions.ts) and
// backend error-code → Turkish message mapping (errors.ts).
//
// Every module that would otherwise need a generated backend-client type
// (a product/order/table DTO) declares a narrow LOCAL structural type instead
// (see e.g. cart.ts's ProductSource) — each app's own generated type (Wails
// wailsjs bindings, a REST client's OpenAPI types, ...) is assignable to it
// with no runtime adapter, because TypeScript structural typing does the work.
// Anything genuinely tied to one app's transport (e.g. pos-desktop's async
// fiscal-registration polling in lib/fiscalStatus.ts, or its Wails event
// wire-shape translation in lib/branchFiscal.ts) stays in that app.

export * from './cart'
export * from './options'
export * from './numpad'
export * from './format'
export * from './payment'
export * from './paymentLines'
export * from './paymentPlan'
export * from './checkActions'
export * from './errors'
