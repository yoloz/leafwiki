import { execSync } from 'child_process'
import { brotliCompressSync, constants, gzipSync } from 'node:zlib'
import react from '@vitejs/plugin-react'
import path from 'path'
import { defineConfig, type ConfigEnv, type Plugin } from 'vite'

// Mirrors scripts/resolve-version.sh's own env-var-override branch before
// falling back to invoking the script. This isn't just an optimization: the
// Docker frontend-build stage only COPYs ./ui/leafwiki-ui/, so the repo-root
// scripts/ directory (and even bash, on node:*-alpine) generally isn't
// present there. Every real build always sets APP_VERSION via --build-arg,
// so checking it first keeps that path working without depending on the
// script being reachable at all; the script invocation remains as the local
// `npm run build`/`npm run dev` fallback (git describe), where the full repo
// and a real shell are available.
export function resolveAppVersion(): string {
  const envVersion = process.env.APP_VERSION?.trim()
  if (envVersion) {
    return envVersion
  }

  try {
    return execSync('./scripts/resolve-version.sh', {
      cwd: path.resolve(__dirname, '../..'),
      encoding: 'utf-8',
      stdio: ['ignore', 'pipe', 'ignore'],
    }).trim()
  } catch {
    return 'v0.1.0'
  }
}

function manualChunks(id: string): string | undefined {
  const normalizedId = id.replaceAll('\\', '/')

  if (
    normalizedId.includes('/node_modules/@codemirror/') ||
    normalizedId.includes('/node_modules/@lezer/') ||
    normalizedId.includes('/node_modules/@fsegurai/')
  ) {
    return 'codemirror'
  }

  return undefined
}

// Go's //go:embed skips files whose names start with "_" or "." when embedding
// a directory, so a chunk like "_baseUniq-*.js" would be missing from the
// binary and 404 at runtime. Strip such prefixes from chunk names.
export function chunkFileName(chunk: { name: string }): string {
  return `static/${chunk.name.replace(/^[._]+/, '')}-[hash].js`
}

// The Go server fills {{__BODY__}} inside #root with server-rendered markup
// (internal/http/shell.go). The dev server serves index.html unprocessed, so
// drop the placeholder there instead of showing it until React mounts.
export function stripDevShellBody(html: string): string {
  return html.replaceAll('{{__BODY__}}', '')
}

const devShellPlaceholders: Plugin = {
  name: 'leafwiki-dev-shell-placeholders',
  apply: 'serve',
  transformIndexHtml: stripDevShellBody,
}

// The Go server picks a precompressed variant by Accept-Encoding
// (internal/http/static.go), so every byte the browser receives on first
// visit is already compressed at build time — no runtime CPU, which matters
// on Raspberry Pi-class hosts. Images and fonts are already compressed
// formats; recompressing them wastes build time.
const precompressibleExtensions = /\.(?:js|mjs|css|svg|json|txt|xml)$/

export function precompressSource(
  fileName: string,
  source: string | Uint8Array,
): { gzip: Uint8Array; brotli: Uint8Array } | undefined {
  if (!precompressibleExtensions.test(fileName)) {
    return undefined
  }
  const data = typeof source === 'string' ? Buffer.from(source, 'utf-8') : Buffer.from(source)
  return {
    gzip: gzipSync(data, { level: 9 }),
    brotli: brotliCompressSync(data, {
      params: { [constants.BROTLI_PARAM_QUALITY]: 9 },
    }),
  }
}

const precompressAssetsPlugin: Plugin = {
  name: 'leafwiki-precompress-assets',
  apply: 'build',
  generateBundle(_options, bundle) {
    // Object.keys snapshot: emitFile adds the .gz/.br variants to the bundle
    // while we iterate, and they must not be recompressed themselves.
    for (const fileName of Object.keys(bundle)) {
      const file = bundle[fileName]
      if (file.type !== 'chunk' && file.type !== 'asset') {
        continue
      }
      const compressed = precompressSource(fileName, file.type === 'chunk' ? file.code : file.source)
      if (!compressed) {
        continue
      }
      this.emitFile({ type: 'asset', fileName: `${fileName}.gz`, source: compressed.gzip })
      this.emitFile({ type: 'asset', fileName: `${fileName}.br`, source: compressed.brotli })
    }
  },
}

// https://vite.dev/config/
export default defineConfig(({ command }: ConfigEnv) => ({
  // Relative base so asset paths in the built HTML are ./static/... instead of /static/...
  // Go rewrites these references to absolute paths at serve time so they resolve correctly
  // when index.html is served for deep SPA routes. Lazy chunks resolve via import.meta.url
  // and therefore work correctly under any sub-path without further server-side patching.
  base: command === 'build' ? './' : '/',
  define: {
    __APP_VERSION__: JSON.stringify(resolveAppVersion()),
  },
  plugins: [react(), devShellPlaceholders, precompressAssetsPlugin],
  server: {
    proxy: {
      '/assets': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        secure: false,
      },
      '/branding': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        secure: false,
      },
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        secure: false,
      },
    },
  },
  optimizeDeps: {
    include: ['mermaid', 'dagre-d3-es'],
  },
  build: {
    assetsDir: 'static', // <--- here you change the target directory
    rollupOptions: {
      output: {
        chunkFileNames: chunkFileName,
        entryFileNames: 'static/[name]-[hash].js',
        assetFileNames: 'static/[name].[hash][extname]',
        manualChunks,
      },
    },
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
}))
