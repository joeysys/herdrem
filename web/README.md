# Web

Vue 3 + Pinia + Vite PWA — the Herdrem web frontend.

- `npm run dev` — Vite dev server
- `npm run build` — type-checks (vue-tsc) and writes the bundle to `../webdist`
- `npm test` — Vitest suite

The relay binary embeds `../webdist` via `go:embed` (`webdist/embed.go`) and serves it at `/`.
