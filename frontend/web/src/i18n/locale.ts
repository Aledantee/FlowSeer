import { watch } from 'vue'
import type { Composer } from 'vue-i18n'
import { supportedLocales } from './index'
import type { WebLocale } from './index'

const STORAGE_KEY = 'flowseer.locale'

function isSupported(value: string | null | undefined): value is WebLocale {
  return supportedLocales.some((locale) => locale === value)
}

// A saved choice wins, then the first browser language whose primary subtag is
// supported, then English. Matching the subtag, not a prefix, keeps `den`
// (Denesuline) from reading as German.
export function resolveLocale(
  saved: string | null,
  languages: readonly string[],
): WebLocale {
  if (isSupported(saved)) return saved
  for (const language of languages) {
    const primary = (language.split('-')[0] ?? '').toLowerCase()
    if (isSupported(primary)) return primary
  }
  return 'en'
}

// Browser storage can be unavailable in restricted browsing contexts, which
// reads as nothing saved.
export function savedLocale(): string | null {
  try {
    return localStorage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
}

// Reports whether the browser kept the choice.
export function saveLocale(locale: WebLocale): boolean {
  try {
    localStorage.setItem(STORAGE_KEY, locale)
    return true
  } catch {
    return false
  }
}

export function initialLocale(): WebLocale {
  return resolveLocale(savedLocale(), navigator.languages)
}

// Keeps `<html lang>` equal to the Composer locale from now on. The returned
// function ends the binding.
export function bindDocumentLang(composer: Pick<Composer, 'locale'>) {
  return watch(
    composer.locale,
    (locale) => {
      document.documentElement.lang = locale
    },
    { immediate: true },
  )
}
