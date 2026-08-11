# webui — orchestrator management UI + API

The `run` command serves this when `web_ui.enabled: true` (see `web_ui.listen_address`
in the config). It exposes an OpenAPI-specified JSON API and an embedded React SPA.

## Layout

- `openapi.yaml` — the API contract (source of truth for both sides).
- `api.gen.go` — generated Go models + strict server (`go generate ./webui/...`).
- `server.go`, `mount.go`, `embed.go` — handler implementation, routing, SPA embedding.
- `frontend/` — Vite + React + TypeScript SPA. `src/api/types.gen.ts` is generated
  from `openapi.yaml` (`npm run gen`).

## Regenerating after an API change

Edit `openapi.yaml`, then:

```sh
# Go side (models + strict server)
go generate ./webui/...

# Frontend side (typed client, then build the embedded bundle)
npm --prefix webui/frontend install      # first time only
npm --prefix webui/frontend run gen       # regenerate src/api/types.gen.ts
npm --prefix webui/frontend run build     # regenerate frontend/dist (embedded by Go)

# Rebuild the binary so the new dist is embedded
go build ./...
```

A placeholder `frontend/dist/index.html` is checked in; the build artifacts
(`dist/assets/` and the built `index.html`) are git-ignored, so `go build` always
compiles and serves the placeholder page until the SPA is built. `npm run build`
overwrites the placeholder locally — don't commit the built `index.html` over it,
since it references the git-ignored `assets/` and would render blank on a fresh
checkout.

## Asset serving and live reload

The compiled SPA is embedded via `go:embed` and served through
`cloudeng.io/webapp/webassets`, so the same binary can either serve the embedded
build or prefer newer files from the local filesystem. Enable reloading in the
`web_ui.reload` config so you can `npm run build` and refresh the browser without
recompiling the Go binary:

```yaml
web_ui:
  enabled: true
  listen_address: 127.0.0.1:8088
  reload:
    reload_enable: true
    reload_new: true
    reload_root: webui   # dir containing frontend/dist (relative to CWD or absolute)
```

`reload_root` is joined with the `frontend/dist` prefix, so it must point at the
`webui` directory (its `frontend/dist` is served in preference to the embedded copy).

## Frontend dev server

For hot-module reload during heavy UI work, use Vite's dev server instead:

```sh
npm --prefix webui/frontend run dev   # proxies /api to 127.0.0.1:8088
```
