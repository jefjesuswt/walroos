# walroos — notes for coding agents

Self-hosted platform on Podman: remote dev workspaces (like Coder/DevPod) + Dokploy-style deploys, with a web UI, an API and MCP. Go single binary + embedded React UI. Networking over Tailscale.

- **Read first:** [PLAN.md](PLAN.md) (status + next tasks) and [ARCHITECTURE.md](ARCHITECTURE.md) (design, in Spanish).
- The owner speaks Spanish: talk to them in Spanish. Code, UI copy, commits and code comments are in English.

## Commands

```bash
cd web && bun install && bun run build && cd ..   # UI → internal/web/ui/dist (embedded, not in git)
CGO_ENABLED=0 go build -o bin/walroos ./cmd/walroos   # static: the SFTP helper also runs in musl images
go test ./...
WALROOS_PODMAN_TEST=1 go test ./internal/agent -v   # + workspace lifecycle on real rootless podman
GOOS=linux CGO_ENABLED=0 go test -c ./internal/agent # SSH tests are linux-only: run the binary in WSL
go run ./cmd/walroos hub --dev                    # 127.0.0.1:8080, no Tailscale, user dev@localhost
cd web && bun run dev                             # Vite :5173, proxies /api to :8080 (WALROOS_API=url for another hub)
```

Podman tests in WSL (Arch has podman): run with `PATH=/usr/local/bin:/usr/bin` (the Windows PATH makes every podman call ~2s slower), and recreate `walroos-dev`, `walroos` and `walroos-edge` with `podman network create --internal <name>` (the WSL kernel lacks the nftables pieces netavark needs for NAT; without internet only `TestWorkspaceMiseToolPersists` fails).

## Layout

```
cmd/walroos/            cobra root; hub/ (control plane), node/ (agent: join, run, telemetry)
config/                 sqlite (WAL, separate read/write pools) + embedded migrations/NNN_*.sql
internal/auth/          Tailscale WhoIs, users and their roles (owner, deployer, developer), permissions
internal/agent/         node agent: hub API (:7070), workspaces and apps (Quadlets) on podman, SSH gateway (:2222), build registry proxy (:5000)
internal/app/           apps: config, source, encrypted env, deployment queue (one at a time) and history, domains, templates
internal/database/      database services (Postgres, MySQL, MariaDB, Redis, Valkey, MongoDB) and their backups
internal/project/       projects: groups of apps and databases sharing a podman network
internal/workspace/     dev workspaces, stack detection, workspaces from scratch
internal/cloudflare/    the bits of Cloudflare's API used: zones, remote-managed tunnels, CNAMEs
internal/fleet/         nodes: models, store, service, handlers, integration tests
internal/sshkeys/       users' SSH keys + the node-facing ssh-auth check
internal/mcp/           MCP at /mcp (go-sdk, stateless): tools over the services, offered by role
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
  - Build screens from the shared UI components (see Rules below). On phones, modals are bottom sheets and row actions go in a ⋮ sheet.
  - Port new screens from the prototype `index.html`, restyled to M3.
  - Check every change at mobile width (no horizontal scroll), in light and dark, and with a non-default theme.
- **Commits:** conventional style (`feat:`, `fix:` …). Work on a branch, not `main`.
- **The repo is public.** Never write the owner's own setup into tracked files, commits or PRs: machine names or hardware, LAN/tailnet IPs, tailnet or hub hostnames, real domains or subdomains, token or tunnel names, other services they run. Use stand-ins (`prod-1`, `example.com`, `100.64.0.x`, "a node with another VPN"). Grep the diff for them before pushing.

## Rules

These hold for every change. Review against them before opening a PR.

### Both
- **Search first.** Before writing a helper, a component or a CSS rule, look for one that already does it, and reuse or extend it.
- **Twice means shared.** When the same markup, logic or check would appear in a second place, make it a component or a function instead of copying it.
- **Remove what you replace.** No dead code, no unused exports, no leftover CSS.

### UI (web/)
- **Use the shared components** for every form row, field, select, page header, section (card), tabs, modal, menu and loading/error/empty state. They live in `web/src/ui/` (until that split lands: `web/src/ui.tsx`). A page composes them; it doesn't rebuild them with raw markup.
- **Fix layout in the component, not the page.** A misaligned row is a bug in `FormRow`/`Field`, fixed once for every screen.
- **No inline styles** (`style={{…}}`) and **no hex colors** in components: only M3 roles (`--md-*` through the aliases) and the spacing and radius tokens. A variant (a `tone`, a size) is a prop on the component.
- **CSS per component area**, class names prefixed by the component (`.popmenu-…`, `.pagehead-…`), so a new class can't collide with an old one.
- **Keep files small:** a page over ~300 lines or a component with many unrelated states gets split (one file per tab, dialogs apart).
- **Dialogs fit the screen:** no scrolling to reach the main fields; secondary options go under a disclosure, long lists scroll inside the dialog.
- **Check** every change at mobile width (no horizontal scroll), in light and dark, and with a non-default theme.

### Backend (Go)
- **Composition over big types:** small types with one job, composed where they're used. No type that grows a method for every feature (split a 30-method service by job: CRUD, queue, integrations).
- **One responsibility per package.** A domain package owns its data, its settings and its handlers; it doesn't import another domain to reach a helper. Shared helpers (node placement, name validation, HTTP error mapping) live in one place and are reused.
- **Short, concrete, cohesive functions.** One function does one step and its name says which. Split anything past ~50 lines into named steps; never write 200–300 line functions. Table literals are the only exception.
- **Errors carry their meaning:** domain errors are sentinel values mapped to a status in one place, not a new switch per package.
- Keep the existing conventions above (stdlib first, no single-implementation interfaces, `// ponytail:` for deliberate limits, one integration test per domain).

## Brand

- **Magenta is the primary color.** Brand magenta is `#E3337C`.
- **Default theme (Walroos):** dark `primary #EE5A96` (text on it `#1A0510`), light `primary #C21F65` (white text). Both are WCAG AA. Other themes change the accent; the logo stays magenta.
- **Logo:** a cute flat baby walrus, three-quarter view, head tilted. The SVGs live in `web/public/`.
- **Don't redo the logo** without being asked. It went through many rejected attempts: single-color silhouettes and negative-space faces read as skulls.

## Decisions to keep

- One owner plus a few trusted guests with fixed roles (owner, deployer, developer). Multitenancy (`cloud.walroos.com`) will be a separate app on top. Don't add tenants to the core.
- The owner is behind CGNAT in Venezuela:
  - App ingress goes through Cloudflare Tunnel.
  - Tailscale Funnel is used only for webhooks.
  - Tailscale (or Headscale) is mandatory.
  - Hub and nodes embed Tailscale with tsnet (userspace, works behind NordVPN). Never require the host's tailscaled.
- Walroos is not a CI engine. The forge runs the tests; walroos deploys on push or when CI calls it with a deploy token.
