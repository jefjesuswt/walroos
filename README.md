<img src="web/public/mascot.svg" width="120" alt="walroos mascot: a baby walrus">

# walroos

Self-hosted dev workspaces and deploys on Podman, over your Tailscale network. See [ARCHITECTURE.md](ARCHITECTURE.md).

**Status: M0** — nodes join a hub and report telemetry; the web UI lists them live.

## Build

```bash
cd web && bun install && bun run build && cd ..   # UI → internal/web/ui/dist (embedded)
go build -o bin/walroos ./cmd/walroos
```

## Run

Hub (first run needs your tailnet login as owner; auth key or the printed login URL joins it to the tailnet):

```bash
TS_AUTHKEY=tskey-auth-... walroos hub --owner you@github
```

Open `http://walroos-hub` from any device on the tailnet → **Nodes → Add node** gives you a join command. On the node (Tailscale up):

```bash
walroos node join --hub walroos-hub --token wlr_join_... --roles dev,build
walroos node run
```

## Develop

```bash
go run ./cmd/walroos hub --dev     # 127.0.0.1:8080, no Tailscale, no auth
cd web && bun run dev              # Vite on :5173, proxies /api to the hub
go test ./...
```

`index.html` at the repo root is the Open Design prototype the UI is ported from.
