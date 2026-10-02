import { createI18n } from 'vue-i18n'
import en from './locales/en.json'
import de from './locales/de.json'

export type WebLocale = 'en' | 'de'
export const supportedLocales = ['en', 'de'] as const

export const numberFormats = {
  en: {
    decimal: {
      style: 'decimal',
      maximumFractionDigits: 20,
    },
    integer: {
      style: 'decimal',
      maximumFractionDigits: 0,
    },
    percent: {
      style: 'percent',
    },
  },
  de: {
    decimal: {
      style: 'decimal',
      maximumFractionDigits: 20,
    },
    integer: {
      style: 'decimal',
      maximumFractionDigits: 0,
    },
    percent: {
      style: 'percent',
    },
  },
} as const

export function createWebI18n(locale: WebLocale = 'en') {
  return createI18n({
    legacy: false,
    locale,
    fallbackLocale: 'en',
    messages: {
      en,
      de,
    },
    numberFormats,
  })
}
