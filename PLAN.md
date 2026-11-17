# Plan

Estado y siguientes pasos. La arquitectura completa está en [ARCHITECTURE.md](ARCHITECTURE.md); este archivo es la lista de trabajo.

## Dónde estamos (2026-11-16)

- **M0 hecho** y mergeado a `main` (PR #1).
  - `walroos hub`: API + UI embebida, sobre tsnet. Auth por Tailscale WhoIs + allowlist (`--owner`).
  - `walroos node join|run`: token de join de un solo uso, heartbeat cada 15s con telemetría (CPU/RAM/disco, versión de Podman, nº de contenedores).
  - UI: página de nodos en vivo, "Add node" con el comando listo para copiar, borrar nodo.
  - Logo (morsita bebé en 3/4) y acento magenta.
- **Verificado en real** (2026-11-16, Arch Linux, Podman 6.2, con NordVPN activo): hub en la tailnet, `node join` + `node run` con telemetría de Podman, usuario fuera de la allowlist → 403.

## Para retomar en otra máquina

Requisitos: Go ≥ 1.27.4 (lo exige `tailscale.com` v1.104), [bun](https://bun.sh), Tailscale (para el modo real), Podman en los nodos Linux.

```bash
cd web && bun install && bun run build && cd ..
go build -o bin/walroos ./cmd/walroos
go test ./...
go run ./cmd/walroos hub --dev        # http://127.0.0.1:8080
```

`internal/web/ui/dist` no está en git: sin `bun run build` el hub responde "UI not built".

### Tailscale detrás de otra VPN (NordVPN, etc.)

Tailscale saca su tráfico por fuera de cualquier VPN (`SO_MARK` / `SO_BINDTODEVICE` a la interfaz por defecto) y el firewall de NordVPN lo descarta: el login se queda colgado.
- **Hub:** ya resuelto en código (`netns.SetEnabled(false)`; tsnet es userspace y no lo necesita).
- **`tailscaled` del host:** usar modo userspace en `/etc/default/tailscaled`:
  `FLAGS="--tun=userspace-networking --socks5-server=localhost:1055 --outbound-http-proxy-listen=localhost:1055"`.
  Sin interfaz `tailscale0`, así que los clientes van por el proxy: `HTTP_PROXY=http://localhost:1055 walroos node run`, `curl -x http://localhost:1055 http://walroos-hub/api/me`.

## Pendiente de M0

- [ ] Nodo con tag (no usuario) → rechazado. Sin probar: hace falta un dispositivo con tag en la tailnet.
- [ ] Unit de systemd `--user` para `walroos node run` (hoy se arranca a mano).

## M1 — Workspaces (siguiente)

Objetivo: desde la laptop, `ssh proyecto.walroos` abre un shell dentro de un contenedor en el homelab y `claude` corre ahí sin instalar nada local.

Tareas, en orden:

1. ✅ **API hub → agente** (`internal/agent`). El agente escucha en `:7070` (todas las interfaces: en modo userspace la tailnet llega por loopback) y acepta solo peticiones cuyo `WhoIs` tenga el StableID del hub, que aprende de la respuesta del heartbeat, más el sha256 del token de nodo como Bearer. Primer endpoint `GET /v1/info`; en el hub, `GET /api/nodes/{id}/agent`.
2. ✅ **Migración `002`**: tablas `workspaces` y `ssh_keys`. Referencian `users(login)` (no hay `user_id`). `foreign_keys` activado en el DSN: un nodo con workspaces no se puede borrar (el servicio de workspaces tendrá que traducir ese error), y borrar un usuario borra sus claves. `ssh_keys.fingerprint` es único: el agente busca por huella.
3. **Agente: crear workspace.**
   - `podman volume create ws-<name>-src` y `ws-<name>-home`.
   - Clonar el repo dentro del volumen src.
   - Leer `.devcontainer/devcontainer.json` (subconjunto: `image`, `build.dockerfile`, `containerEnv`, `remoteUser`, `forwardPorts`, `postCreateCommand`, `mounts`).
   - Build o pull de la imagen; `podman run -d` con label `walroos.workspace=<name>`.
   - Start/stop/delete.
4. **Imagen base** de walroos para repos sin devcontainer: Debian + git + curl + mise + `openssh-sftp-server`.
5. **SSH gateway en el agente** (`golang.org/x/crypto/ssh`, puerto 2222).
   - Usuario SSH = nombre del workspace.
   - Sesión → `podman exec -it`. Soporta `pty`, `exec`, `direct-tcpip` y el subsistema `sftp`.
   - Claves públicas: el agente las pide al hub y las cachea.
6. **Hub: servicio de workspaces** (elige nodo con rol `dev`, llama al agente, guarda estado).
7. **CLI `walroos ssh-config`**: imprime los bloques `Host <ws>.walroos` para `~/.ssh/config`.
8. **UI**:
   - Página Workspaces: lista, modal "crear desde repo", info de conexión (SSH / VS Code / Claude Code).
   - Ajustes → claves SSH.

Decisiones abiertas de M1:
- Podman por CLI (como en M0) o por la API REST del socket. Default: seguir con CLI hasta que duela.
- `sftp`: usar el `sftp-server` de la imagen vía `podman exec`, o `pkg/sftp` en el agente. Default: el de la imagen. Si la imagen no lo tiene, usar `pkg/sftp`.
- Dónde guarda el agente su host key SSH (sugerido: junto a `node.json`, `0600`).

## Después

| Hito | Resumen |
|---|---|
| M2 MCP | `/mcp` en el hub con herramientas de nodos y workspaces. |
| M3 Deploy | Apps, build + registry en el nodo `build`, Quadlets, red `walroos`, Cloudflare Tunnel, zero-downtime, rollback. |
| M4 CI/CD | Webhooks vía Funnel, `on_push` / `on_ci`, deploy tokens. |
| M5 | Previews por PR, compose, features de devcontainer, Caddy. |

## Backlog chico

- Self-host de las fuentes (hoy Google Fonts; offline cae a fuentes del sistema).
- Selector de tema claro/oscuro en la UI (el CSS ya soporta `data-theme="light"`).
- Pantallas placeholder del sidebar (Overview, Apps, …) se rellenan con su hito.
