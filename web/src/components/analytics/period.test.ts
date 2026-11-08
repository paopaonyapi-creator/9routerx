// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
import { describe, expect, it } from 'bun:test'
import { PERIODS, normalizeCustomPeriod, periodLabel, rememberPeriod, sanitizeStoredPeriod, storedPeriod } from './types'

describe('period label', () => {
  it('names every preset the selector offers', () => {
    for (const preset of PERIODS) {
      expect(periodLabel(preset.value)).toBe(preset.label)
    }
  })

  it('offers the unbounded window the issue asked for', () => {
    expect(PERIODS.some((p) => p.value === 'all')).toBe(true)
  })

  it('labels a custom window rather than showing the raw value', () => {
    expect(periodLabel('14d')).toBe('Last 14 days')
    expect(periodLabel('12h')).toBe('Last 12 hours')
  })

  it('never renders blank for a value it does not recognise', () => {
    expect(periodLabel('garbage')).toBe('garbage')
  })
})

describe('custom period input', () => {
  it('accepts days and hours', () => {
    expect(normalizeCustomPeriod('14d')).toBe('14d')
    expect(normalizeCustomPeriod('12h')).toBe('12h')
  })

  it('normalizes spacing and case', () => {
    expect(normalizeCustomPeriod('  30D ')).toBe('30d')
  })

  it('rejects input the server could not resolve to a window', () => {
    // Sending these would silently read as the 7-day fallback server-side,
    // showing the user a number that is not the window they asked for.
    for (const bad of ['', '   ', 'd', '14', '14x', '-3d', '0d', 'abc']) {
      expect(normalizeCustomPeriod(bad)).toBeNull()
    }
  })

  it('caps the window at ten years', () => {
    expect(normalizeCustomPeriod('3650d')).toBe('3650d')
    expect(normalizeCustomPeriod('87600h')).toBe('87600h')
    // A five-digit day count is a typo, not an analysis window; the selector
    // rejects it with the capped message instead of charting 99999 buckets.
    expect(normalizeCustomPeriod('3651d')).toBeNull()
    expect(normalizeCustomPeriod('87601h')).toBeNull()
    expect(normalizeCustomPeriod('99999d')).toBeNull()
  })
})

function withPeriodStorage(run: (values: Map<string, string>) => void) {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const values = new Map<string, string>()
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      localStorage: {
        getItem: (key: string) => values.get(key) ?? null,
        setItem: (key: string, value: string) => values.set(key, value),
      },
    },
  })
  try {
    run(values)
  } finally {
    if (previous) Object.defineProperty(globalThis, 'window', previous)
    else Reflect.deleteProperty(globalThis, 'window')
  }
}

describe('stored period', () => {
  it('validates presets and bounded custom windows', () => {
    for (const preset of PERIODS) expect(sanitizeStoredPeriod(preset.value)).toBe(preset.value)
    expect(sanitizeStoredPeriod(' 12H ')).toBe('12h')
    for (const bad of ['abc', '-5', '0', '', '99999d', '87601h']) {
      expect(sanitizeStoredPeriod(bad)).toBeNull()
    }
  })

  it('keeps each section independent and falls back for corrupt values', () => {
    withPeriodStorage((values) => {
      expect(storedPeriod('usage', 'today')).toBe('today')
      expect(storedPeriod('cache', '24h')).toBe('24h')
      rememberPeriod('usage', '12h')
      rememberPeriod('cache', '14d')
      rememberPeriod('compression', '30d')
      expect(storedPeriod('usage', 'today')).toBe('12h')
      expect(storedPeriod('cache', '24h')).toBe('14d')
      expect(storedPeriod('compression', '24h')).toBe('30d')
      values.set('9routerx-analytics-period-usage', '99999d')
      expect(storedPeriod('usage', 'today')).toBe('today')
    })
  })

  it('survives unavailable browser storage', () => {
    const previous = Object.getOwnPropertyDescriptor(globalThis, 'window')
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: {
        get localStorage() { throw new Error('storage unavailable') },
      },
    })
    try {
      expect(() => storedPeriod('usage', 'today')).not.toThrow()
      expect(storedPeriod('usage', 'today')).toBe('today')
      expect(() => rememberPeriod('usage', '12h')).not.toThrow()
    } finally {
      if (previous) Object.defineProperty(globalThis, 'window', previous)
      else Reflect.deleteProperty(globalThis, 'window')
    }
  })
})