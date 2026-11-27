import '@testing-library/jest-dom'
import { i18nReady } from './lib/i18n'

// Locale bundles load lazily per language (see lib/i18n.ts). Component tests
// render synchronously, so make sure the default language's namespaces are
// in place before any test file runs. Switching languages in a test awaits
// i18next.changeLanguage, which loads the target language on demand.
await i18nReady

if (typeof window !== 'undefined' && !window.localStorage) {
  const storage = new Map<string, string>()

  Object.defineProperty(window, 'localStorage', {
    value: {
      clear: () => storage.clear(),
      getItem: (key: string) => storage.get(key) ?? null,
      removeItem: (key: string) => storage.delete(key),
      setItem: (key: string, value: string) => {
        storage.set(key, value)
      },
    },
    configurable: true,
  })
}
