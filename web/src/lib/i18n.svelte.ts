// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
import idDict from '../locales/id.json'

export type Locale = 'en' | 'id'

export const SUPPORTED_LOCALES: { id: Locale; label: string; flag: string }[] = [
  { id: 'en', label: 'English', flag: '🇺🇸' },
  { id: 'id', label: 'Bahasa Indonesia', flag: '🇮🇩' },
]

const dictionaries: Record<Locale, Record<string, string>> = {
  en: {},
  id: idDict as Record<string, string>,
}

let currentLocale = $state<Locale>('en')

if (typeof window !== 'undefined') {
  const saved = localStorage.getItem('9routerx-locale') as Locale | null
  if (saved && (saved === 'en' || saved === 'id')) {
    currentLocale = saved
  }
}

export function getLocale(): Locale {
  return currentLocale
}

export function setLocale(loc: Locale) {
  if (loc === 'en' || loc === 'id') {
    currentLocale = loc
    if (typeof window !== 'undefined') {
      localStorage.setItem('9routerx-locale', loc)
      localStorage.setItem('locale', loc)
    }
  }
}

/**
 * Translate a string literal into current active locale.
 * Falls back to the English source text if translation is missing.
 */
export function t(key: string): string {
  if (currentLocale === 'en' || !key) return key
  const dict = dictionaries[currentLocale]
  if (dict && dict[key]) {
    return dict[key]
  }
  return key
}
