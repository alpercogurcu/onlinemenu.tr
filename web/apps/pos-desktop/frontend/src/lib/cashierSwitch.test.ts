import { describe, expect, it } from 'vitest'
import {
  canSwitchTo,
  findOwnParticipant,
  isValidPinFormat,
  pinsMatch,
  toParticipantView,
  type ParticipantView,
} from './cashierSwitch'

const withPin: ParticipantView = { personId: 'p1', fullName: 'Ayşe Yılmaz', hasPin: true, locked: false }
const noPin: ParticipantView = { personId: 'p2', fullName: 'Mehmet Demir', hasPin: false, locked: false }
const lockedOut: ParticipantView = { personId: 'p3', fullName: 'Fatma Kaya', hasPin: true, locked: true }

describe('toParticipantView', () => {
  it('maps the snake_case wire shape to camelCase', () => {
    expect(toParticipantView({ person_id: 'p1', full_name: 'Ayşe Yılmaz', has_pin: true, locked: false })).toEqual(
      withPin,
    )
  })
})

describe('canSwitchTo', () => {
  it('allows a participant with a pin who is not locked', () => {
    expect(canSwitchTo(withPin)).toBe(true)
  })

  it('refuses a participant who never set a pin', () => {
    expect(canSwitchTo(noPin)).toBe(false)
  })

  it('refuses a locked participant even though they have a pin', () => {
    expect(canSwitchTo(lockedOut)).toBe(false)
  })
})

describe('isValidPinFormat', () => {
  it('accepts 4, 5, and 6 digit pins', () => {
    expect(isValidPinFormat('1234')).toBe(true)
    expect(isValidPinFormat('12345')).toBe(true)
    expect(isValidPinFormat('123456')).toBe(true)
  })

  it('rejects too short or too long', () => {
    expect(isValidPinFormat('123')).toBe(false)
    expect(isValidPinFormat('1234567')).toBe(false)
  })

  it('rejects non-digit characters', () => {
    expect(isValidPinFormat('12a4')).toBe(false)
    expect(isValidPinFormat('12 4')).toBe(false)
  })

  it('rejects an empty pin', () => {
    expect(isValidPinFormat('')).toBe(false)
  })
})

describe('pinsMatch', () => {
  it('matches identical non-empty values', () => {
    expect(pinsMatch('1234', '1234')).toBe(true)
  })

  it('does not match differing values', () => {
    expect(pinsMatch('1234', '1235')).toBe(false)
  })

  it('does not match two empty strings — nothing has been typed yet', () => {
    expect(pinsMatch('', '')).toBe(false)
  })
})

describe('findOwnParticipant', () => {
  const participants = [withPin, noPin]

  it('finds the matching participant by personId', () => {
    expect(findOwnParticipant(participants, 'p2')).toEqual(noPin)
  })

  it('returns null when personId is not in the list', () => {
    expect(findOwnParticipant(participants, 'nobody')).toBeNull()
  })

  it('returns null when personId is undefined', () => {
    expect(findOwnParticipant(participants, undefined)).toBeNull()
  })
})
