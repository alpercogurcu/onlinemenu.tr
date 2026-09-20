import { describe, expect, it } from 'vitest'
import {
  applyNumpadKey,
  formatMoneyInputDisplay,
  isNumpadKeyEnabled,
  kurusToMoneyInput,
  type NumpadKey,
  type NumpadMode,
} from './numpad'

function press(mode: NumpadMode, keys: NumpadKey[], start = '', maxLength?: number): string {
  return keys.reduce((value, key) => applyNumpadKey(value, key, { mode, maxLength }), start)
}

describe('applyNumpadKey — money', () => {
  it.each([
    { name: 'whole lira digits', keys: ['2', '0', '0'] as NumpadKey[], want: '200' },
    { name: 'double zero appends two digits', keys: ['5', '00'] as NumpadKey[], want: '500' },
    { name: 'leading zero is collapsed', keys: ['0', '5'] as NumpadKey[], want: '5' },
    { name: 'double zero on empty stays zero', keys: ['00'] as NumpadKey[], want: '0' },
    { name: 'comma on empty starts with zero', keys: [','] as NumpadKey[], want: '0,' },
    { name: 'kuruş digits after comma', keys: ['1', '2', ',', '5'] as NumpadKey[], want: '12,5' },
    { name: 'second comma is ignored', keys: ['1', ',', '5', ','] as NumpadKey[], want: '1,5' },
    { name: 'third decimal is ignored', keys: ['1', ',', '2', '5', '9'] as NumpadKey[], want: '1,25' },
    { name: 'double zero fills the missing decimals', keys: ['1', '2', ',', '5', '00'] as NumpadKey[], want: '12,50' },
    { name: 'double zero after bare comma fills both decimals', keys: ['1', ',', '00'] as NumpadKey[], want: '1,00' },
    { name: 'double zero on full decimals is a no-op', keys: ['1', ',', '2', '5', '00'] as NumpadKey[], want: '1,25' },
    { name: 'backspace removes the last char', keys: ['1', '2', 'backspace'] as NumpadKey[], want: '1' },
    { name: 'backspace over the comma', keys: ['1', ',', 'backspace'] as NumpadKey[], want: '1' },
    { name: 'backspace on empty stays empty', keys: ['backspace'] as NumpadKey[], want: '' },
  ])('$name', ({ keys, want }) => {
    expect(press('money', keys)).toBe(want)
  })

  it('caps the whole-lira part at 7 digits', () => {
    expect(press('money', ['1', '2', '3', '4', '5', '6', '7', '8', '00'])).toBe('1234567')
  })

  it('never exceeds the cap with the double-zero key', () => {
    expect(press('money', ['1', '2', '3', '4', '5', '6', '00'])).toBe('123456')
  })
})

describe('applyNumpadKey — integer', () => {
  it.each([
    { name: 'digits accumulate', keys: ['1', '2'] as NumpadKey[], want: '12' },
    { name: 'leading zero collapses', keys: ['0', '7'] as NumpadKey[], want: '7' },
    { name: 'double zero on empty is a no-op', keys: ['00'] as NumpadKey[], want: '0' },
    { name: 'double zero after a digit', keys: ['3', '00'] as NumpadKey[], want: '300' },
    { name: 'comma is ignored', keys: ['3', ','] as NumpadKey[], want: '3' },
  ])('$name', ({ keys, want }) => {
    expect(press('integer', keys)).toBe(want)
  })

  it('honours maxLength', () => {
    expect(press('integer', ['1', '2', '3', '4', '5'], '', 4)).toBe('1234')
    expect(press('integer', ['00'], '99', 3)).toBe('99')
  })
})

describe('applyNumpadKey — pin', () => {
  it('keeps leading zeros — a PIN is a code, not a number', () => {
    expect(press('pin', ['0', '1', '2', '3'])).toBe('0123')
  })

  it('caps at maxLength', () => {
    expect(press('pin', ['1', '2', '3', '4', '5', '6', '7'], '', 6)).toBe('123456')
  })

  it('ignores comma and double zero', () => {
    expect(press('pin', ['1', ',', '00', '2'])).toBe('12')
  })

  it('backspace removes the last digit', () => {
    expect(press('pin', ['1', '2', '3', 'backspace'])).toBe('12')
  })
})

describe('applyNumpadKey — pendingReplace (a preset chip just filled the field)', () => {
  const replace = { mode: 'money' as const, pendingReplace: true }

  it('starts a fresh amount on a digit instead of appending to the preset', () => {
    expect(applyNumpadKey('200,00', '5', replace)).toBe('5')
  })

  it('starts a fresh amount on the comma key', () => {
    expect(applyNumpadKey('200,00', ',', replace)).toBe('0,')
  })

  it('deletes one character on backspace — it must NOT wipe the field, or a blank amount would silently mean "the whole remaining balance"', () => {
    expect(applyNumpadKey('200,00', 'backspace', replace)).toBe('200,0')
  })

  it('applies to integer counts too', () => {
    expect(applyNumpadKey('12', '3', { mode: 'integer', pendingReplace: true })).toBe('3')
    expect(applyNumpadKey('12', 'backspace', { mode: 'integer', pendingReplace: true })).toBe('1')
  })

  it('changes nothing when the flag is off', () => {
    expect(applyNumpadKey('1', '5', { mode: 'money' })).toBe('15')
  })
})

describe('isNumpadKeyEnabled', () => {
  it('shows comma and double zero only where they mean something', () => {
    expect(isNumpadKeyEnabled('money', ',')).toBe(true)
    expect(isNumpadKeyEnabled('money', '00')).toBe(true)
    expect(isNumpadKeyEnabled('integer', ',')).toBe(false)
    expect(isNumpadKeyEnabled('integer', '00')).toBe(true)
    expect(isNumpadKeyEnabled('pin', ',')).toBe(false)
    expect(isNumpadKeyEnabled('pin', '00')).toBe(false)
    expect(isNumpadKeyEnabled('pin', '7')).toBe(true)
  })
})

describe('kurusToMoneyInput', () => {
  it.each([
    { kurus: 0, want: '0,00' },
    { kurus: 5000, want: '50,00' },
    { kurus: 12345, want: '123,45' },
    { kurus: 5, want: '0,05' },
  ])('$kurus -> $want', ({ kurus, want }) => {
    expect(kurusToMoneyInput(kurus)).toBe(want)
  })
})

describe('formatMoneyInputDisplay', () => {
  it.each([
    { value: '', want: '0' },
    { value: '5', want: '5' },
    { value: '1234', want: '1.234' },
    { value: '1234567', want: '1.234.567' },
    { value: '1234,5', want: '1.234,5' },
    { value: '0,', want: '0,' },
  ])('"$value" -> "$want"', ({ value, want }) => {
    expect(formatMoneyInputDisplay(value)).toBe(want)
  })
})
