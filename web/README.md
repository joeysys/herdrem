# Web

Vue 3 + Pinia + Vite PWA — the Herdrem web frontend.

- `npm run dev` — Vite dev server
- `npm run build` — type-checks (vue-tsc) and writes the bundle to `../webdist`
- `npm test` — Vitest suite

`HERDREM_PWA_NAME` is read only while this frontend is built. It sets the manifest `name` and `short_name`, the document title, and `apple-mobile-web-app-title`, so two systems can install two labels. Unset or blank stays `Herdrem`. Tabs and newlines become spaces; other control characters are dropped. The result is trimmed and capped at 48 characters. Putting it in `herdrem.env` does nothing: the relay never reads it, and an already built UI keeps the old label until you rebuild and add the icon again.

```bash
HERDREM_PWA_NAME='Acme Ops' npm run build
```

The relay binary embeds `../webdist` via `go:embed` (`webdist/embed.go`) and serves it at `/`.
