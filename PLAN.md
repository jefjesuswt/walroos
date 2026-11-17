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
3. ✅ **Agente: crear workspace** (`internal/agent/workspaces.go`, API `/v1/workspaces`).
   - Podman es la fuente de verdad: contenedor `ws-<name>` con label `walroos.workspace`, volúmenes `ws-<name>-src` (repo) y `ws-<name>-home` (`$HOME`). En memoria solo lo que está en curso (`creating`/`deleting`) y el último error (`failed`).
   - Crear es asíncrono (`202`): `git` del host clona directo al volumen, se lee `devcontainer.json` (JSONC, subconjunto), build o pull, y `podman run` con `--userns=keep-id:uid=U,gid=G` para que el usuario del host sea el `remoteUser` dentro (uid/gid/`$HOME` se preguntan a la imagen).
   - Start/stop/delete síncronos; delete borra contenedor, volúmenes e imagen construida.
   - Usuario: `remoteUser` de `devcontainer.json`, si no el de la label `devcontainer.metadata` de la imagen (las `mcr.microsoft.com/devcontainers/*` declaran p. ej. `node` ahí), si no el usuario por defecto de la imagen. De la label solo se toma el usuario.
   - `mounts` del devcontainer: solo `type=volume` y `type=tmpfs`; `bind` se rechaza para que un repo no pueda montar rutas del host.
   - Límites (`ponytail:`): sin credenciales de git (solo repos públicos o token en la URL), `postCreateCommand` en forma objeto no soportado, sin docker compose, estado `failed` se pierde si reinicia el agente.
   - Tests con podman real: `WALROOS_PODMAN_TEST=1 go test ./internal/agent -v`.
4. ✅ **Imagen base** (`internal/agent/base/Containerfile`, embebida): Debian 13 slim + git, curl, ssh, `openssh-sftp-server`, sudo y mise (versión fija, sha256 verificado, amd64/arm64). Usuario `dev` (uid 1000, sudo sin contraseña) declarado en `devcontainer.metadata`, así que corre como `dev` sin código extra.
   - El agente la construye en local la primera vez que un repo no trae devcontainer (~1 min, una vez por nodo), con un mutex para creaciones simultáneas. Tag `localhost/walroos-base:<hash del Containerfile>`: si el Containerfile cambia, se reconstruye y se borran las versiones viejas que ningún workspace usa.
   - Runtimes de mise en `~/.local/share/mise` (volumen del home): sobreviven a reconstruir el contenedor. Shims en el `PATH` para sesiones no interactivas.
   - Incluye `libicu` (.NET revienta al arrancar sin ICU) y `/etc/profile.d/mise.sh` (el `/etc/profile` de Debian pisa el `PATH` en login shells, como una sesión SSH interactiva).
   - ~420 MB; 171 MB son el binario de mise. Probado con DotnetApp real: clon por SSH, `mise use dotnet@10`, `dotnet build` OK.
   - El agente solo toca contenedores y volúmenes con su label `walroos.workspace`: un `ws-<name>` ajeno ni se reutiliza ni se borra.
5. **SSH gateway en el agente** (`golang.org/x/crypto/ssh`, puerto 2222).
   - Usuario SSH = nombre del workspace.
   - Sesión → `podman exec -it`. Soporta `pty`, `exec`, `direct-tcpip` y el subsistema `sftp`.
   - Claves públicas: el agente las pide al hub y las cachea.
   - **Agent forwarding** (`ssh -A`, petición `auth-agent-req@openssh.com`): `git push` desde el workspace usa las llaves del portátil, sin copiar llaves al contenedor. El socket tiene que existir dentro del contenedor (p. ej. un directorio del agente montado al crear el workspace, `SSH_AUTH_SOCK` apuntando ahí), así que puede requerir tocar `create` de la tarea 3. Descubierto probando DotnetApp: el clon usa las llaves del nodo, pero el contenedor no tiene ninguna.
6. **Hub: servicio de workspaces** (elige nodo con rol `dev`, llama al agente, guarda estado).
7. **CLI `walroos ssh-config`**: imprime los bloques `Host <ws>.walroos` para `~/.ssh/config`.
8. **UI**:
   - Página Workspaces: lista, modal "crear desde repo", info de conexión (SSH / VS Code / Claude Code).
   - Ajustes → claves SSH.

Tareas descubiertas probando con DotnetApp (2026-11-16):

9. **Rebuild sin perder el home.** Hoy, cambiar el devcontainer (o recibir una imagen base nueva) obliga a borrar y crear, y eso borra el volumen del home: runtimes de mise, login de `claude`, historial. Rebuild = borrar contenedor e imagen construida, conservar `-src` y `-home`, volver a resolver imagen y usuario, `podman run` y `postCreateCommand`. Agente (`POST /v1/workspaces/{name}/rebuild`) + hub + botón en la UI.
10. **Variables y secretos por workspace.** Lo que no está en git (`.env`, `MSSQL_SA_PASSWORD`, cadenas de conexión) hoy hay que copiarlo a mano. El hub los guarda cifrados (mismo esquema que `app_env` de M3: AES-GCM, clave en archivo `0600`) y el agente los inyecta al crear o reconstruir (`podman run --env-file` desde un archivo temporal `0600`, borrado después). Decidir si además se escribe un `.env` en la carpeta del repo para proyectos que lo leen de ahí.
11. **Servicios del nodo desde el workspace.** Proyectos como DotnetApp necesitan una base de datos (SQL Server con compose). Mientras no haya compose (M5), el servicio corre aparte en el nodo. Verificar y documentar cómo llega el workspace a él: `host.containers.internal` con la red rootless (pasta) y un puerto publicado solo en `127.0.0.1` del host. Si no llega, decidir la receta (publicar en la IP de la tailnet, o una red podman compartida `walroos-dev`).

Notas de uso (para el README cuando haya flujo completo):
- No meter en un workspace credenciales de **producción**: el contenedor vive en un nodo del homelab y lo ve cualquiera con acceso al nodo. Para proyectos que trabajan contra la BD de producción (un proyecto .NET), usar una BD de desarrollo.
- Proyectos sin devcontainer arrancan en la imagen base y el runtime se instala con `mise use` (una vez, persiste en el home). Mejor añadir un `devcontainer.json` con la imagen del stack (p. ej. `mcr.microsoft.com/devcontainers/dotnet:10.0`) para que cada workspace nuevo nazca listo.

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
