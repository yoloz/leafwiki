# AGENTS.md

Guidance for AI agents working in this repository. Humans start at
[CONTRIBUTING.md](./CONTRIBUTING.md); the rules are the same.

If `AGENTS.local.md` exists in the repository root, read it next. It holds
maintainer-local instructions, is never committed, and wins where it conflicts
with this file.

## The project

LeafWiki is a self-hosted wiki: Markdown files on disk are the source of truth,
SQLite indexes are derived and rebuildable, one binary for Linux, **Windows**,
macOS and **Raspberry Pi**.
Backend: layered Go (`cmd/leafwiki` → `internal/wiki` composition root → slices →
`internal/core/*`). Frontend: React SPA in `ui/leafwiki-ui` with feature-local Zustand stores.

Read before changing code — **[docs/development/](./docs/development/README.md)**:
[rules](./docs/development/rules.md) (the short list, same for people and agents),
[conventions](./docs/development/conventions.md) (the reference behind them),
[source map](./docs/development/source-map.md), [architecture](./docs/development/architecture.md),
[processes](./docs/development/processes.md), [ADRs](./docs/development/adr/README.md).

## Stack

- Backend: Go, gin, SQLite (FTS5 search), shortid page IDs
- Frontend: React + TypeScript + Vite + Tailwind CSS v4 (CSS-first, no config file) + zustand + react-i18next (de/en/es/fr/zh)
- Markdown: react-markdown 10 (remark-gfm/math, rehype-highlight/katex/raw/sanitize), highlight.js, KaTeX, Mermaid (lazy-loaded)

## Commands

| What | Command | Where |
|---|---|---|
| Local subset of the PR checks: verify, lint, test, cross, ui-check (not 386 tests, govulncheck, Trivy, install-script test, e2e lint, E2E, Dev Container build) | `make ci` | repo root |
| Backend tests / lint | `make test` / `make lint` (golangci-lint at the version in `.golangci-lint-version`) | repo root |
| Repository checks (private references, toolchain versions) | `make verify` | repo root |
| Windows/macOS build check | `make cross` (build + vet for windows/amd64, darwin/arm64) | repo root |
| Frontend install + lint + format check + tests + build | `make ui-check` | repo root |
| Frontend typecheck + build | `npm run build` (not `npx tsc --noEmit`, it checks nothing) | `ui/leafwiki-ui` |
| Frontend tests / lint / format | `npm test` / `npm run lint -- --max-warnings=0` / `npm run format` | `ui/leafwiki-ui` |
| E2E | `make run-e2e`; lint/format in `e2e` | repo root |
| Local dev | `make dev` (both, Ctrl-C stops both), or backend: `go run . --jwt-secret=dev --admin-password=devpassword --public-access=true --allow-insecure=true` in `cmd/leafwiki`; frontend: `npm run dev` | |

A fresh worktree has no `node_modules`: run `make setup` (`npm ci --ignore-scripts` in
`ui/leafwiki-ui` and `e2e`) first; `make doctor` checks the Go and Node versions. Toolchain
versions have one source each: Go in `go.mod`'s `toolchain` line, Node in `.nvmrc`;
`make verify` checks that the Dockerfiles and `mise.toml` match.

Raw fallbacks (when not using `make`):
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
- Body font is LXGW WenKai (+ WenKai Mono for code) served from the jsDelivr CDN: `<link>` tags live in `index.html`, the font stacks in `index.css` (`--font-sans` and the `.code, pre, code` rule)
- Tests are colocated (`*.test.tsx`), vitest + jsdom

## Gotchas

- Frontmatter is split server-side; the preview never sees it (editor-only frontmatter panel)
- `MarkdownPreview` shoutout titles are hardcoded English (not i18n'd)
- Mermaid is dynamically imported (`useMermaidInjector`), `securityLevel: antiscript`
- Raw HTML is sanitized (`rehype-sanitize` + `rehypeWhitelistStyles` style whitelist)
- Size caps: urlimport 10 MiB per page/image, assets 50 MiB default (`assets.DefaultMaxUploadSizeBytes`)
- `POST /api/pages/import-url` only extracts; `POST /api/pages/import-url/create` creates the page and localizes referenced images as attachments (failures keep the original URL)

## Rules

The rules live in **[docs/development/rules.md](./docs/development/rules.md)**, nowhere else.
Read them before changing code; don't restate them in prompts, skills or roles, link there.
Much of it is enforced in CI (import boundaries, ESLint XSS guard, i18n key parity,
golangci-lint, Prettier, Windows/macOS cross build, private references); a red check is a real
finding. Commit subjects are checked too (`make verify-commits BASE=origin/main`), as
information: messy contributor PRs get squash-merged.

## Commits and pull requests

- **Committing locally is fine** when the history is clean: one logical change per commit,
  Conventional Commits (`type(scope): description`), refactoring, behaviour change and
  `test: tidy` in separate commits, formatters run, tests green. Never commit secrets,
  generated files or local overlays.
- **Pushing, opening PRs and commenting on GitHub need the person's explicit OK.**
- PR size and scope: [rules](./docs/development/rules.md) and
  [conventions](./docs/development/conventions.md#pull-requests); process in
  [CONTRIBUTING.md](./CONTRIBUTING.md#pull-request-guidelines) and
  [AI-assisted contributions](./CONTRIBUTING.md#ai-assisted-contributions).

## Workflow and roles

Changes run through independent roles, scaled by tier (details in the `develop` skill):
**trivial** (typo, rename, docs) → implement + CI; **normal** (change inside one slice) →
implement + tester + architecture-reviewer + reviewer; **feature** (new feature, extension
point, API or format change) → all roles with a plan gate; **+ security** for anything touching
rendering, uploads, served files, auth, routes or paths.

Roles in `.claude/agents/` (plain Markdown prompts): `planner`, `architecture-reviewer`,
`implementer`, `reviewer`, `tester`, `security-reviewer`. Each runs with a fresh context; the
checking roles see only the plan and the diff. Without subagent support, run each role in its
own session with its file as instructions.

## Skills

Runbooks in `.claude/skills/<name>/SKILL.md`: `develop` (the workflow above), `tidy-tests`
(restructure a package's tests with proof nothing was lost), `run` (start backend + frontend),
`docs-review` (docs vs. code), `worktree-cleanup`.
