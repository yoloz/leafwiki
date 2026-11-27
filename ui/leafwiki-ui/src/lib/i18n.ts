import i18next, { type BackendModule, type ReadCallback } from 'i18next'
import { initReactI18next } from 'react-i18next'
// Side-effect: keeps date-fns / Intl locale in sync with the active language.
import './dateLocale'
// Side-effect: keeps <html lang> in sync with the active language.
import './documentLang'

// Lazy entry per locale file: only the language actually in use is fetched.
// The entry chunk previously bundled every language, so first paint grew
// with each new locale; pulling the active language's namespaces over HTTP
// (precompressed, see the Go /static handler) keeps it independent of the
// language count.
const localeModules = import.meta.glob<{ default: Record<string, unknown> }>(
  '../locales/*/*.json',
)

// common stays eager: the language picker lists every language's self name
// before any language bundle is loaded, and it is the fallback surface for
// very early strings.
const commonModules = import.meta.glob<{ default: Record<string, unknown> }>(
  '../locales/*/common.json',
  { eager: true },
)

const LOCALE_PATH_PATTERN = /\.\.\/locales\/([^/]+)\/([^/]+)\.json$/

const resources: Record<string, Record<string, Record<string, unknown>>> = {}
for (const path in commonModules) {
  const match = LOCALE_PATH_PATTERN.exec(path)
  if (!match) continue
  const [, lang, namespace] = match
  resources[lang] ??= {}
  resources[lang][namespace] = commonModules[path].default
}

// Namespaces are identical across languages (enforced by the key-parity test
// in i18n.test.ts), so the set can be derived from the file listing alone.
const allNamespaces = [
  ...new Set(
    Object.keys(localeModules)
      .map((path) => LOCALE_PATH_PATTERN.exec(path)?.[2])
      .filter((namespace): namespace is string => Boolean(namespace)),
  ),
]

export type AvailableLanguage = {
  code: string
  name: string
}

function selfNameOf(lang: string): string {
  const language = resources[lang]?.common?.language as
    { selfName?: string } | undefined
  return language?.selfName ?? lang
}

export function getAvailableLanguages(): AvailableLanguage[] {
  return Object.keys(resources)
    .sort()
    .map((code) => ({ code, name: selfNameOf(code) }))
}

// LazyLocaleBackend fetches namespace files on demand through the glob above.
// i18next drives it for every missing language/namespace pair — both from
// useTranslation mounts and from changeLanguage — so callers need no loading
// logic of their own.
class LazyLocaleBackend implements BackendModule {
  type = 'backend' as const

  init(): void {}

  read(language: string, namespace: string, callback: ReadCallback): void {
    const load = localeModules[`../locales/${language}/${namespace}.json`]
    if (!load) {
      // Unknown combination (the key-parity test keeps languages in sync, so
      // this only happens for a truly unknown language): report nothing, so
      // i18next falls back to the fallback language's bundles.
      callback(null, undefined)
      return
    }
    load()
      .then((mod) => callback(null, mod.default))
      .catch((err) =>
        callback(err instanceof Error ? err : String(err), undefined),
      )
  }
}

// init only wires the instances; partialBundledLanguages keeps the eager
// common bundles from marking every namespace as loaded, so backend reads
// still happen for everything else.
const initPromise = i18next
  .use(initReactI18next)
  .use(new LazyLocaleBackend())
  .init({
    lng: 'en',
    fallbackLng: 'en',
    ns: allNamespaces,
    partialBundledLanguages: true,
    resources,
    interpolation: {
      escapeValue: false,
    },
  })

// i18nReady resolves once the default language's namespaces have loaded;
// main.tsx awaits it before mounting so the first paint never shows raw keys.
export const i18nReady = initPromise.then(() =>
  i18next.loadNamespaces(allNamespaces),
)

export default i18next
