# AGENTS.md

Guidance for AI agents working in this repository. LeafWiki is a Markdown wiki:
Go backend with an embedded React SPA.

## Stack

- Backend: Go, gin, SQLite (FTS5 search), shortid page IDs
- Frontend: React + TypeScript + Vite + Tailwind CSS v4 (CSS-first, no config file) + zustand + react-i18next (de/en/es/fr/zh)
- Markdown: react-markdown 10 (remark-gfm/math, rehype-highlight/katex/raw/sanitize), highlight.js, KaTeX, Mermaid (lazy-loaded)

## Commands

- Backend (repo root): `go build ./...`, `go test ./internal/...`; keep gofmt clean and run `go vet`
- Frontend: `cd ui/leafwiki-ui` → `npm ci` (node_modules is not committed) → `./node_modules/.bin/tsc -b`, `./node_modules/.bin/vitest run`, `./node_modules/.bin/eslint <files>`
- E2E: separate `e2e/` package (Playwright)
- Commits: Conventional Commits in English, e.g. `feat(import): ...`

## Layout

- `internal/core/` — domain services: `tree` (pages/nodes), `assets`, `revision`, `markdown` (frontmatter), `auth`, `search`, `links`, `tags`, `properties`, `shared` (LocalizedError, atomic writes)
- `internal/wiki/<domain>/` — per-domain use cases + gin routes (pages, assets, revisions, auth, importer, backup, snapshot, restore, ...)
- `internal/wiki/wiki.go` — `NewWiki` wires everything; `build<Domain>Routes(options)` constructors; `buildRoutes(options)` registers all
- `internal/http/` — gin engine, middleware, `RouterOptions` (e.g. `MaxAssetUploadSizeBytes`)
- `internal/urlimport/` — SSRF-safe web page fetch/extract (private-IP dial guard, redirect scheme check, 10 MiB caps)
- `internal/importer/` — Obsidian/zip import; `content_transformer.go` rewrites links and uploads assets
- `ui/src/features/<feature>/` — React features (preview, editor, page, admin, ...)
- `ui/src/lib/` — API clients (`api/*.ts` mirror backend endpoints), zustand stores, i18n glue
- `ui/src/locales/{de,en,es,fr,zh}/<namespace>.json` — translations; `viewer.json` holds preview strings
- `ui/src/index.css` — all global CSS (~3900 lines): design tokens, component styles, print layer

## Backend conventions

- Use case pattern: `NewXUseCase(deps...)` + `Execute(ctx, XInput) (XOutput, error)` with Input/Output structs
- Errors: `sharederrors.NewLocalizedError(code, userMsg, logTemplate, err, args...)`; HTTP mapping lives in each domain's `errors.go` (`respondWithPageError` etc.); sentinel errors via `errors.Is`
- Validation: `sharederrors.NewValidationErrors()` → 400 `{error: "validation_error", fields}`
- Optimistic locking: page mutations require `Version` (`tree.Page.Version()` = `Metadata.UpdatedAt`, RFC3339Nano)
- Routes: domain `RoutesConfig` → `RegisterRoutes(RouterContext)`; reads via `APIReadGroup` (public-mode aware), writes via `APIAuthGroup` + `authmw.RequireEditorOrAdmin()`
- Assets: disk `<storageDir>/assets/<pageID>/<file>`, markdown reference `/assets/<pageID>/<file>`; uploads record a revision
- Page content lives at `<storageDir>/root/<parent path>/<slug>.md` with managed frontmatter

## Frontend conventions

- Markdown preview pipeline: `src/features/preview/MarkdownPreview.tsx` — preprocessing (`preprocessWikilinks` for `[[..]]`, `normalizeMarkdownBlocks` for `:::` directives) → remark/rehype chain → custom renderers (shoutouts, TOC, footnotes, wikilinks, images, PDF, code)
- Three preview surfaces must stay visually in sync; CSS rules are triplicated today — prefer `:is(.page-viewer__content, .markdown-editor__preview, .page-history__preview-body)` for new rules
- Design tokens: HSL triplets (`--brand`, `--border`, `--surface-alt`, ...) in `index.css` `:root` / `.dark`; the `.dark` class is toggled by the `designmode` zustand store (`useApplyDesignMode`)
- Tailwind v4 CSS-first: `@theme` + `@plugin` blocks live in `index.css`; `@tailwindcss/typography` `prose` is the markdown base
- The CodeMirror 6 editor shares `MarkdownPreview` and the hljs theme CSS with the reading view
- Tests are colocated (`*.test.tsx`), vitest + jsdom

## Gotchas

- Frontmatter is split server-side; the preview never sees it (editor-only frontmatter panel)
- `MarkdownPreview` shoutout titles are hardcoded English (not i18n'd)
- Mermaid is dynamically imported (`useMermaidInjector`), `securityLevel: antiscript`
- Raw HTML is sanitized (`rehype-sanitize` + `rehypeWhitelistStyles` style whitelist)
- Size caps: urlimport 10 MiB per page/image, assets 50 MiB default (`assets.DefaultMaxUploadSizeBytes`)
- `POST /api/pages/import-url` only extracts; `POST /api/pages/import-url/create` creates the page and localizes referenced images as attachments (failures keep the original URL)
