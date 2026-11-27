import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('child_process', async (importOriginal) => {
  const actual = await importOriginal<typeof import('child_process')>()
  return {
    ...actual,
    execSync: vi.fn(() => {
      throw new Error(
        'scripts/resolve-version.sh not reachable (simulated Docker frontend-build stage)',
      )
    }),
  }
})

import { brotliDecompressSync, gunzipSync } from 'node:zlib'
import {
  chunkFileName,
  precompressAssetsPlugin,
  precompressSource,
  resolveAppVersion,
  stripDevShellBody,
} from './vite.config'

describe('resolveAppVersion', () => {
  const originalAppVersion = process.env.APP_VERSION

  afterEach(() => {
    if (originalAppVersion === undefined) {
      delete process.env.APP_VERSION
    } else {
      process.env.APP_VERSION = originalAppVersion
    }
  })

  it('returns APP_VERSION without needing scripts/resolve-version.sh to be reachable', () => {
    // The Docker frontend-build stage only COPYs ui/leafwiki-ui/, so
    // scripts/resolve-version.sh (and bash, on node:*-alpine) generally
    // isn't present there. Every real build sets APP_VERSION via
    // --build-arg regardless, so this must not depend on the script
    // succeeding.
    process.env.APP_VERSION = 'v9.9.9-test'

    expect(resolveAppVersion()).toBe('v9.9.9-test')
  })
})

describe('chunkFileName', () => {
  it('never emits a file name starting with "_" or "."', () => {
    // Go's //go:embed skips files starting with "_" or "." when embedding a
    // directory, so such a chunk (e.g. mermaid's lodash "_baseUniq") would
    // 404 at runtime and break the lazy import that depends on it.
    expect(chunkFileName({ name: '_baseUniq' })).toBe(
      'static/baseUniq-[hash].js',
    )
    expect(chunkFileName({ name: '.hidden' })).toBe('static/hidden-[hash].js')
  })

  it('leaves regular chunk names untouched', () => {
    expect(chunkFileName({ name: 'codemirror' })).toBe(
      'static/codemirror-[hash].js',
    )
  })
})

describe('stripDevShellBody', () => {
  it('empties #root in the dev server, where Go does not fill the shell', () => {
    // The Go server replaces {{__BODY__}} with contributed markup; the Vite
    // dev server serves index.html as is, so the placeholder would flash as
    // text until React mounts.
    expect(
      stripDevShellBody(
        '<title>{{__PAGE_TITLE__}}</title><div id="root">{{__BODY__}}</div>',
      ),
    ).toBe('<title>{{__PAGE_TITLE__}}</title><div id="root"></div>')
  })
})

describe('precompressAssetsPlugin', () => {
  it('runs after Vite resolves the __VITE_PRELOAD__ markers', () => {
    // Vite's build-import-analysis uses a plain generateBundle hook. A plain
    // hook here would compress the code before the markers were replaced, so
    // browsers (which prefer .br/.gz) would execute an undefined identifier.
    // Post-order is what keeps compressed variants byte-identical to .js.
    expect(precompressAssetsPlugin.generateBundle).toMatchObject({
      order: 'post',
    })
  })
})

describe('precompressSource', () => {
  const js = 'function f(){return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}\n'.repeat(
    20,
  )

  it('compresses scripts into gzip and brotli variants', () => {
    const compressed = precompressSource('static/index-abc123.js', js)
    expect(compressed).toBeDefined()
    // Round-trip, so the Go server can serve these bytes verbatim with the
    // matching Content-Encoding.
    expect(gunzipSync(compressed!.gzip).toString()).toBe(js)
    expect(brotliDecompressSync(compressed!.brotli).toString()).toBe(js)
    expect(compressed!.gzip.byteLength).toBeLessThan(Buffer.byteLength(js))
    expect(compressed!.brotli.byteLength).toBeLessThan(
      compressed!.gzip.byteLength,
    )
  })

  it('compresses assets given as bytes', () => {
    const svg = Buffer.from('<svg><rect/></svg>'.repeat(10))
    const compressed = precompressSource('static/logo.abc123.svg', svg)
    expect(gunzipSync(compressed!.gzip).equals(svg)).toBe(true)
  })

  it('skips already-compressed formats and the emitted variants themselves', () => {
    // Fonts and images are compressed formats, and the .gz/.br artifacts the
    // plugin emits must never be fed back through the compressor.
    expect(precompressSource('static/font.abc123.woff2', 'x')).toBeUndefined()
    expect(precompressSource('static/logo.abc123.png', 'x')).toBeUndefined()
    expect(precompressSource('static/index-abc123.js.gz', 'x')).toBeUndefined()
    expect(precompressSource('static/index-abc123.js.br', 'x')).toBeUndefined()
  })
})
