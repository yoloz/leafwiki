import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App.tsx'
import './index.css'
import { isSSRPreview } from './lib/ssrPreview'

import '@fontsource/inter/400.css'
import '@fontsource/inter/500.css'
import '@fontsource/inter/600.css'
import '@fontsource/inter/700.css'
import '@fontsource/inter/800.css'
import '@fontsource/inter/900.css'
import '@fontsource/inter/400-italic.css'
import '@fontsource/inter/500-italic.css'
import '@fontsource/inter/600-italic.css'
import '@fontsource/inter/700-italic.css'

// Noto Sans SC (body) + JetBrains Mono (code), bundled locally so the wiki
// works without any external CDN request. Import the Chinese-simplified and
// latin subsets explicitly rather than the all-subset CSS: a single CJK
// subset file covers the glyphs without pulling in every script variant.
import '@fontsource/noto-sans-sc/chinese-simplified-400.css'
import '@fontsource/noto-sans-sc/chinese-simplified-700.css'
import '@fontsource/noto-sans-sc/latin-400.css'
import '@fontsource/noto-sans-sc/latin-700.css'
import '@fontsource/jetbrains-mono/latin-400.css'
import '@fontsource/jetbrains-mono/latin-700.css'

// Locale bundles load lazily (see lib/i18n.ts); don't paint before the
// default language's namespaces are in place, or the first frame shows keys.
import { i18nReady } from './lib/i18n'

const root = document.getElementById('root')!

if (!isSSRPreview(window.location.search, root)) {
  await i18nReady
  createRoot(root).render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
}
