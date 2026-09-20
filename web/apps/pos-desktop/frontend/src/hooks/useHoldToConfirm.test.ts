import { describe, expect, it } from 'vitest'
import { isKeyboardActivation } from './useHoldToConfirm'

describe('isKeyboardActivation', () => {
  it('treats a click with detail 0 (Enter/Space/assistive activation) as a deliberate confirm', () => {
    expect(isKeyboardActivation(0)).toBe(true)
  })

  it('does not confirm on a pointer click — touch and mouse must still hold', () => {
    expect(isKeyboardActivation(1)).toBe(false)
    expect(isKeyboardActivation(2)).toBe(false)
  })
})
