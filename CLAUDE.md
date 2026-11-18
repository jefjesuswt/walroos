# walroos — notes for Claude Code

Self-hosted platform on Podman: remote dev workspaces (like Coder/DevPod) + Dokploy-style deploys, with a web UI, an API and MCP. Go single binary + embedded React UI. Networking over Tailscale.

- **Read first:** [PLAN.md](PLAN.md) (status + next tasks) and [ARCHITECTURE.md](ARCHITECTURE.md) (design, in Spanish).
- The owner speaks Spanish: talk to them in Spanish. Code, UI copy, commits and code comments are in English.

## Commands

```bash
cd web && bun install && bun run build && cd ..   # UI → internal/web/ui/dist (embedded, not in git)
go build -o bin/walroos ./cmd/walroos
go test ./...
WALROOS_PODMAN_TEST=1 go test ./internal/agent -v   # + workspace lifecycle on real rootless podman
GOOS=linux CGO_ENABLED=0 go test -c ./internal/agent # SSH tests are linux-only: run the binary in WSL
go run ./cmd/walroos hub --dev                    # 127.0.0.1:8080, no Tailscale, user dev@localhost
cd web && bun run dev                             # Vite :5173, proxies /api to :8080
```

Podman tests in WSL (Arch has podman): run with `PATH=/usr/local/bin:/usr/bin` (the Windows PATH makes every podman call ~2s slower), and recreate `walroos-dev` with `podman network create --internal walroos-dev` (the WSL kernel lacks the nftables pieces netavark needs for NAT; without internet only `TestWorkspaceMiseToolPersists` fails).

## Layout

```
cmd/walroos/            cobra root; hub/ (control plane), node/ (agent: join, run, telemetry)
config/                 sqlite (WAL, separate read/write pools) + embedded migrations/NNN_*.sql
internal/auth/          Tailscale WhoIs + users allowlist middleware
internal/agent/         node agent: hub API (:7070), workspaces and apps (Quadlets) on podman, SSH gateway (:2222)
internal/app/           apps: config, encrypted env, deployment queue (one at a time) and history
internal/fleet/         nodes: models, store, service, handlers, integration tests
internal/sshkeys/       users' SSH keys + the node-facing ssh-auth check
internal/mcp/           MCP at /mcp (go-sdk, stateless): tools over the fleet and workspace services
internal/web/           serves the embedded SPA (SPA fallback, JSON 404 for /api/*)
internal/version/
shared/whttp/           router + JSON helpers
web/                    React 18 + Vite + TS UI; styles.css copied from the Open Design prototype
web/public/             favicon.svg (logo icon), mascot.svg (full mascot)
index.html (root)       Open Design prototype the UI is ported from; reference for upcoming screens
```

## Conventions

- **Minimal code.** Use the stdlib and the deps already in go.mod before adding anything. No interfaces with a single implementation. Mark deliberate shortcuts with a `// ponytail:` comment that names the limit.
- **Domain pattern** like `internal/fleet`: `models.go`, `store.go`, `service.go`, `handlers.go`, plus one integration test against a real temp SQLite and the real router.
- **Migrations:** add `config/migrations/00N_name.sql`. Never edit an applied migration.
- **Tokens** are random and only their sha256 is stored. Prefixes: `wlr_join_*`, `wlr_node_*`.
- **Podman** goes through the CLI (`exec.Command("podman", ...)`). The podman/v4 Go bindings don't build on Windows and were removed.
- **UI:**
  - Material 3 look, plain CSS in `web/src/styles.css`. No Tailwind, no MUI or `@material/web`.
  - Colors are only M3 roles (`--md-*`, via the aliases at the top of `styles.css`), never a hex: themes swap them.
  - Themes live in `web/src/theme.ts`: presets (Walroos, Catppuccin, Dracula, Aura) plus imported Material Theme Builder JSON. Presets must pass `contrastWarnings` (WCAG AA).
  - Reuse components from `web/src/ui.tsx` (Modal, Confirm, Field, Cmd, CopyBtn, Badge, Meter, EmptyState, usePoll, toasts). On phones, modals are bottom sheets and row actions go in a ⋮ sheet.
  - Port new screens from the prototype `index.html`, restyled to M3.
  - Check every change at mobile width (no horizontal scroll), in light and dark, and with a non-default theme.
- **Commits:** conventional style (`feat:`, `fix:` …). Work on a branch, not `main`.

## Brand

- **Magenta is the primary color.** Brand magenta is `#E3337C`.
- **Default theme (Walroos):** dark `primary #EE5A96` (text on it `#1A0510`), light `primary #C21F65` (white text). Both are WCAG AA. Other themes change the accent; the logo stays magenta.
- **Logo:** a cute flat baby walrus, three-quarter view, head tilted. The SVGs live in `web/public/`.
- **Don't redo the logo** without being asked. It went through many rejected attempts: single-color silhouettes and negative-space faces read as skulls.

## Decisions to keep

- Single user in v1. Multitenancy (`cloud.walroos.com`) will be a separate app on top. Don't add tenants to the core.
- The owner is behind CGNAT in Venezuela:
  - App ingress goes through Cloudflare Tunnel.
  - Tailscale Funnel is used only for webhooks.
  - Tailscale (or Headscale) is mandatory.
  - Hub and nodes embed Tailscale with tsnet (userspace, works behind NordVPN). Never require the host's tailscaled.
- Walroos is not a CI engine. The forge runs the tests; walroos deploys on push or when CI calls it with a deploy token.
