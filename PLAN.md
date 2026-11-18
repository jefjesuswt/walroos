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

## M1 — Workspaces ✅ (2026-11-17)

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
   - ~420 MB; 171 MB son el binario de mise. Probado con gPay.Net real: clon por SSH, `mise use dotnet@10`, `dotnet build` OK.
   - El agente solo toca contenedores y volúmenes con su label `walroos.workspace`: un `ws-<name>` ajeno ni se reutiliza ni se borra.
5. ✅ **SSH gateway en el agente** (`internal/agent/ssh.go`, `golang.org/x/crypto/ssh`, `--ssh-listen :2222`).
   - Usuario SSH = nombre del workspace. Solo peers de la tailnet (WhoIs antes del handshake). Host key ed25519 junto a `node.json` (`ssh_host_ed25519_key`, `0600`); el fingerprint sale en el log al arrancar.
   - Claves: `internal/sshkeys` en el hub (`GET/POST/DELETE /api/ssh-keys`). El agente pregunta `POST /api/nodes/{id}/ssh-auth` con su token de nodo (huella + workspace → login) y cachea el sí 1 min. El owner entra a cualquier workspace; un member solo a los suyos (`workspaces.owner`).
   - Sesión → `podman exec -i [-t] --user U -w <carpeta del repo>`, con el login shell del usuario (`getent`). Soporta `pty` (+ resize), `exec`, `env`, `direct-tcpip` (`-L`) y el subsistema `sftp` (el `sftp-server` de la imagen; si no hay, error claro, sin `pkg/sftp`).
   - `-L`: `podman unshare nsenter --net=/proc/PID/ns/net -- walroos node relay HOST:PORT`, o sea, llega a los puertos del contenedor, no a los del host.
   - **Agent forwarding** (`ssh -A`): `create` monta `$XDG_RUNTIME_DIR/walroos/<ws>` en `/run/walroos` (label `walroos.agentdir`). Por conexión, un socket `0600` ahí y `SSH_AUTH_SOCK` apuntando a él; se borra al desconectar.
   - Un workspace parado arranca solo al conectar.
   - Probar: añadir la clave con `curl -X POST http://walroos-hub/api/ssh-keys -d "{\"public_key\":\"$(cat ~/.ssh/id_ed25519.pub)\"}"` y `ssh -A -p 2222 <ws>@<nodo>`.
   - Tests: `ssh_test.go` (todo menos podman, Linux) y `ssh_podman_test.go` (`WALROOS_PODMAN_TEST=1`). **Probado de punta a punta por la tailnet** (2026-11-17, Arch + Podman 6.2 rootless, `tailscaled --tun=userspace-networking`, cliente tsnet como laptop): `ssh`/`exec`, `sftp` (imagen base), `-L` al puerto del contenedor, `-A` (`/run/walroos/*.sock` 0600 y `ssh-add -l` ve la clave), `-R` rechazado, arranque automático si estaba parado, clave no registrada → `Permission denied`, conexión directa fuera de la tailnet → rechazada, host key estable tras reiniciar el agente.
   - En modo userspace el peer llega como `127.0.0.1:<puerto>` y `WhoIs` resuelve por el 4-tupla mientras la conexión está abierta: por eso la identidad se conserva. Una conexión directa a `:2222` no tiene esa entrada y se rechaza.
   - Límites (`ponytail:`): workspaces creados antes de esto no tienen directorio del agente (sin `-A` hasta recrearlos o la tarea 9); `-R` (remote forward) rechazado; si podman ignora el SIGHUP puede quedar el proceso del `exec`; sin `XDG_RUNTIME_DIR` el directorio cae a `/tmp/walroos-<uid>`.
6. ✅ **Hub: servicio de workspaces** (`internal/workspace`, `GET/POST /api/workspaces`, `GET/DELETE /api/workspaces/{name}`, `POST .../start|stop`).
   - Elige el nodo `dev` **online** con menos workspaces, o el que se pida por nombre/ID (si no tiene `dev` → 409). Llama al agente y guarda la fila.
   - `List`/`Get` refrescan el estado desde el agente y lo persisten (crear es asíncrono: `creating` → `running`/`failed`). Si el agente no responde, se muestra la última fila conocida.
   - Si el agente acepta pero el hub no puede guardar la fila, borra el workspace del nodo para no dejar huérfanos.
   - Borrar un nodo con workspaces → 409 con mensaje (antes era un 500 del FK).
   - Probado de punta a punta con podman real (hub dev + agente con el token del nodo): crear 201 → `running`, stop/start, delete y sin contenedores ni volúmenes. En `--dev` el agente rechaza al hub (sin identidad de tailnet): es lo esperado.
7. ✅ **CLI `walroos ssh-config`**: imprime `Host <name>.walroos` (HostName = nombre MagicDNS del nodo, Port 2222, User = workspace) para `~/.ssh/config`. `--proxy <addr>` añade `ProxyCommand nc -X 5 -x <addr> %h %p` (openbsd-netcat) y usa el mismo proxy para hablar con el hub: la máquina en modo userspace. `--port` y `--suffix` configurables. Se autentica con la identidad de tailnet, sin token. Probado de punta a punta: workspace real, config generada, `ssh app.walroos` entra como `dev` con el repo montado.
8. ✅ **UI**: página Workspaces (lista con estado, modal "nuevo desde repo" con nombre, repo, rama, nodo y devcontainer_path, tarjeta de conexión con `ssh <name>.walroos`, VS Code y túneles, start/stop/delete con confirmación) y Ajustes → claves SSH (listar, añadir con la clave pública, borrar). Reutiliza `ui.tsx`, tabla y modales existentes. Probado con podman real y el hub dev: crear desde la UI → `running`, borrar, clave listada, ambos temas.

Tareas descubiertas probando con gPay.Net (2026-11-16):

9. ✅ **Rebuild sin perder el home.** `POST /v1/workspaces/{name}/rebuild` (agente) y `POST /api/workspaces/{name}/rebuild` (hub), con botón en la UI. Borra el contenedor y la imagen construida (`localhost/walroos-ws-<name>`), conserva `-src` y `-home`, y vuelve a resolver imagen/usuario y ejecutar `postCreateCommand`. El spec (rama, devcontainer_path) se guarda en `$HOME/.walroos/workspace.json`; si falta (workspaces viejos), se usa el remoto de git. Probado con podman real: un cambio de `remoteUser` en el devcontainer se aplica, el contenedor cambia de ID y un archivo en `$HOME` sobrevive; de punta a punta por el hub, 202 → `running`.
10. ✅ **Variables y secretos por workspace.** Tabla `workspace_env` (migración 003), valores cifrados con AES-256-GCM; la clave vive en `<data>/secret.key` (0600, junto a `hub.db`: hay que respaldarla con la base). `GET/PUT /api/workspaces/{name}/env` (el PUT reemplaza todo el conjunto), `secret` solo cambia cómo lo muestra la UI. El agente recibe `env` al crear y al reconstruir y lo pasa con `podman run --env-file` desde un archivo temporal `0600` (los valores no aparecen en `argv`); en el rebuild el hub los reenvía porque el agente no los guarda. La UI tiene un editor por workspace y avisa de que se aplican al reconstruir. Probado: PUT/GET, validación, cascada al borrar, cifrado en la BD, y con podman real un `GREETING=hola` visible dentro del contenedor tras el rebuild.
11. **Servicios del nodo desde el workspace.** Proyectos como gPay.Net necesitan una base de datos (SQL Server con compose). Mientras no haya compose (M5), el servicio corre aparte en el nodo. Verificar y documentar cómo llega el workspace a él: `host.containers.internal` con la red rootless (pasta) y un puerto publicado solo en `127.0.0.1` del host. Si no llega, decidir la receta (publicar en la IP de la tailnet, o una red podman compartida `walroos-dev`).

Notas de uso (para el README cuando haya flujo completo):
- No meter en un workspace credenciales de **producción**: el contenedor vive en un nodo del homelab y lo ve cualquiera con acceso al nodo. Para proyectos que trabajan contra la BD de producción (gPay.Net, REGLA 3), usar una BD de desarrollo.
- Proyectos sin devcontainer arrancan en la imagen base y el runtime se instala con `mise use` (una vez, persiste en el home). Mejor añadir un `devcontainer.json` con la imagen del stack (p. ej. `mcr.microsoft.com/devcontainers/dotnet:10.0`) para que cada workspace nuevo nazca listo.

Decisiones abiertas de M1:
- Podman por CLI (como en M0) o por la API REST del socket. Default: seguir con CLI hasta que duela.

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
