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
CGO_ENABLED=0 go build -o bin/walroos ./cmd/walroos
go test ./...
go run ./cmd/walroos hub --dev        # http://127.0.0.1:8080
```

`internal/web/ui/dist` no está en git: sin `bun run build` el hub responde "UI not built".

### Tailscale detrás de otra VPN (NordVPN, etc.)

Tailscale saca su tráfico por fuera de cualquier VPN (`SO_MARK` / `SO_BINDTODEVICE` a la interfaz por defecto) y el firewall de NordVPN lo descarta: el login se queda colgado.
- **Hub y nodo:** resuelto en código. Los dos llevan Tailscale embebido (`tsnet`, userspace) con `netns.SetEnabled(false)`. El nodo es `walroos-<nombre>` en la tailnet y guarda su estado en `~/.config/walroos/tsnet-node`; no hace falta `tailscaled` en la máquina.
- **`tailscaled` del host como cliente** (la laptop desde la que haces `ssh`): usar modo userspace en `/etc/default/tailscaled`:
  `FLAGS="--tun=userspace-networking --socks5-server=localhost:1055 --outbound-http-proxy-listen=localhost:1055"`.
  Sin interfaz `tailscale0`, así que los clientes van por el proxy: `walroos ssh-config --proxy localhost:1055`, `curl -x http://localhost:1055 http://walroos-hub/api/me`.

## Nodo con tsnet (2026-11-18)

`node join` y `node run` levantan su propio `tsnet` (como el hub) en vez de usar el `tailscaled` del host: un binario, sin instalar Tailscale y funciona con NordVPN. API del agente (`:7070`) y gateway SSH (`:2222`) escuchan solo en la tailnet. `ssh-config` usa la IP de tailnet del nodo (la del heartbeat) como `HostName`, porque el nombre en la tailnet ahora es `walroos-<nombre>`. Los nodos que ya existían tienen que volver a hacer `join` (cambia su identidad en la tailnet).

## Pendiente de M0

- [ ] Nodo con tag (no usuario) → rechazado. Sin probar: hace falta un dispositivo con tag en la tailnet.
- [x] **`walroos node install`** (2026-11-18): escribe `~/.config/systemd/user/walroos-node.service` (`Restart=on-failure`), lo activa y arranca, e intenta `loginctl enable-linger`. `loginctl` sale con 0 aunque polkit lo rechace (sin sesión activa, p. ej. `wsl -- …`), así que se relee `Linger` y, si no quedó, imprime el `sudo loginctl enable-linger <usuario>`. Probado en WSL Arch con systemd: servicio activo, heartbeats con telemetría de podman, `kill -9` → reiniciado a los 10 s.

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
   - Límites (`ponytail:`): sin credenciales de git (solo repos públicos o token en la URL; las URLs SSH `git@host:…` se clonan por https y el workspace deja la SSH como `origin`), `postCreateCommand` en forma objeto no soportado, sin docker compose, estado `failed` se pierde si reinicia el agente.
   - Tests con podman real: `WALROOS_PODMAN_TEST=1 go test ./internal/agent -v`.
4. ✅ **Imagen base** (`internal/agent/base/Containerfile`, embebida): Debian 13 slim + git, curl, ssh, `openssh-sftp-server`, sudo y mise (versión fija, sha256 verificado, amd64/arm64). Usuario `dev` (uid 1000, sudo sin contraseña) declarado en `devcontainer.metadata`, así que corre como `dev` sin código extra.
   - El agente la construye en local la primera vez que un repo no trae devcontainer (~1 min, una vez por nodo), con un mutex para creaciones simultáneas. Tag `localhost/walroos-base:<hash del Containerfile>`: si el Containerfile cambia, se reconstruye y se borran las versiones viejas que ningún workspace usa.
   - Runtimes de mise en `~/.local/share/mise` (volumen del home): sobreviven a reconstruir el contenedor. Shims en el `PATH` para sesiones no interactivas.
   - Incluye `libicu` (.NET revienta al arrancar sin ICU) y `/etc/profile.d/mise.sh` (el `/etc/profile` de Debian pisa el `PATH` en login shells, como una sesión SSH interactiva).
   - ~420 MB; 171 MB son el binario de mise. Probado con DotnetApp real: clon por SSH, `mise use dotnet@10`, `dotnet build` OK.
   - El agente solo toca contenedores y volúmenes con su label `walroos.workspace`: un `ws-<name>` ajeno ni se reutiliza ni se borra.
5. ✅ **SSH gateway en el agente** (`internal/agent/ssh.go`, `golang.org/x/crypto/ssh`, `--ssh-listen :2222`).
   - Usuario SSH = nombre del workspace. Solo peers de la tailnet (WhoIs antes del handshake). Host key ed25519 junto a `node.json` (`ssh_host_ed25519_key`, `0600`); el fingerprint sale en el log al arrancar.
   - Claves: `internal/sshkeys` en el hub (`GET/POST/DELETE /api/ssh-keys`). El agente pregunta `POST /api/nodes/{id}/ssh-auth` con su token de nodo (huella + workspace → login) y cachea el sí 1 min. El owner entra a cualquier workspace; un member solo a los suyos (`workspaces.owner`).
   - Sesión → `podman exec -i [-t] --user U -w <carpeta del repo>`, con el login shell del usuario (`getent`). Soporta `pty` (+ resize), `exec`, `env`, `direct-tcpip` (`-L`) y el subsistema `sftp` (el `sftp-server` de la imagen; si no hay, error claro, sin `pkg/sftp`).
   - `-L`: `podman unshare nsenter --net=/proc/PID/ns/net -- walroos node relay /proc/PID/root HOST:PORT`, o sea, llega a los puertos del contenedor, no a los del host. El relay hace `chroot` al filesystem del workspace, así que los nombres se resuelven como dentro (su DNS, su `/etc/hosts`, `host.containers.internal`).
   - Cada sesión tiene su propio contexto: cerrar una sesión cuelga su proceso aunque la conexión siga abierta (VS Code, `ControlMaster`). Al caerse la conexión, podman propaga el SIGHUP y el proceso muere (probado con `exec` y con terminal).
   - Si el hub rechaza una clave o no responde, el agente lo registra (`SSH: key refused`, con el error). Hub caído → login rechazado al momento; hub colgado → rechazado a los 10 s; los fallos no se cachean.
   - **Agent forwarding** (`ssh -A`): `create` monta `$XDG_RUNTIME_DIR/walroos/<ws>` en `/run/walroos` (label `walroos.agentdir`). Por conexión, un socket `0600` ahí y `SSH_AUTH_SOCK` apuntando a él; se borra al desconectar.
   - Un workspace parado arranca solo al conectar.
   - Probar: añadir la clave con `curl -X POST http://walroos-hub/api/ssh-keys -d "{\"public_key\":\"$(cat ~/.ssh/id_ed25519.pub)\"}"` y `ssh -A -p 2222 <ws>@<nodo>`.
   - Tests: `ssh_test.go` (todo menos podman, Linux) y `ssh_podman_test.go` (`WALROOS_PODMAN_TEST=1`). **Probado de punta a punta por la tailnet** (2026-11-17, Arch + Podman 6.2 rootless, `tailscaled --tun=userspace-networking`, cliente tsnet como laptop): `ssh`/`exec`, `sftp` (imagen base), `-L` al puerto del contenedor, `-A` (`/run/walroos/*.sock` 0600 y `ssh-add -l` ve la clave), `-R` rechazado, arranque automático si estaba parado, clave no registrada → `Permission denied`, conexión directa fuera de la tailnet → rechazada, host key estable tras reiniciar el agente.
   - En modo userspace el peer llega como `127.0.0.1:<puerto>` y `WhoIs` resuelve por el 4-tupla mientras la conexión está abierta: por eso la identidad se conserva. Una conexión directa a `:2222` no tiene esa entrada y se rechaza.
   - Casos tristes probados con podman real (WSL Arch, Podman 6.2): imagen de devcontainer sin `sftp-server` (exit 127 y mensaje claro), `-A` en un workspace sin directorio del agente (se rechaza el agente, la sesión funciona), `-L` a puerto cerrado (rechazado al momento) y a host desconocido, conexión cortada durante `exec` y durante terminal, sesión cerrada con la conexión abierta.
   - Límites (`ponytail:`): workspaces creados antes de esto no tienen directorio del agente (sin `-A` hasta recrearlos o rebuild); `-R` (remote forward) rechazado; tras un half-close del cliente el relay espera a que la app cierre su lado; una clave borrada en el hub sigue entrando hasta 1 min (caché); sin `XDG_RUNTIME_DIR` el directorio cae a `/tmp/walroos-<uid>`.
6. ✅ **Hub: servicio de workspaces** (`internal/workspace`, `GET/POST /api/workspaces`, `GET/DELETE /api/workspaces/{name}`, `POST .../start|stop`).
   - Elige el nodo `dev` **online** con menos workspaces, o el que se pida por nombre/ID (si no tiene `dev` → 409). Llama al agente y guarda la fila.
   - `List`/`Get` refrescan el estado desde el agente y lo persisten (crear es asíncrono: `creating` → `running`/`failed`). Si el agente no responde, se muestra la última fila conocida.
   - Si el agente acepta pero el hub no puede guardar la fila, borra el workspace del nodo para no dejar huérfanos.
   - Borrar un nodo con workspaces → 409 con mensaje (antes era un 500 del FK).
   - Probado de punta a punta con podman real (hub dev + agente con el token del nodo): crear 201 → `running`, stop/start, delete y sin contenedores ni volúmenes. En `--dev` el agente rechaza al hub (sin identidad de tailnet): es lo esperado.
7. ✅ **CLI `walroos ssh-config`**: imprime `Host <name>.walroos` (HostName = nombre MagicDNS del nodo, Port 2222, User = workspace) para `~/.ssh/config`. `--proxy <addr>` añade `ProxyCommand nc -X 5 -x <addr> %h %p` (openbsd-netcat) y usa el mismo proxy para hablar con el hub: la máquina en modo userspace. `--port` y `--suffix` configurables. Se autentica con la identidad de tailnet, sin token. Probado de punta a punta: workspace real, config generada, `ssh app.walroos` entra como `dev` con el repo montado.
8. ✅ **UI**: página Workspaces (lista con estado, modal "nuevo desde repo" con nombre, repo, rama, nodo y devcontainer_path, tarjeta de conexión con `ssh <name>.walroos`, VS Code y túneles, start/stop/delete con confirmación) y Ajustes → claves SSH (listar, añadir con la clave pública, borrar). Reutiliza `ui.tsx`, tabla y modales existentes. Probado con podman real y el hub dev: crear desde la UI → `running`, borrar, clave listada, ambos temas.

Tareas descubiertas probando con DotnetApp (2026-11-16):

9. ✅ **Rebuild sin perder el home.** `POST /v1/workspaces/{name}/rebuild` (agente) y `POST /api/workspaces/{name}/rebuild` (hub), con botón en la UI. Borra el contenedor y la imagen construida (`localhost/walroos-ws-<name>`), conserva `-src` y `-home`, y vuelve a resolver imagen/usuario y ejecutar `postCreateCommand`. El spec (rama, devcontainer_path) se guarda en `$HOME/.walroos/workspace.json`; si falta (workspaces viejos), se usa el remoto de git. Probado con podman real: un cambio de `remoteUser` en el devcontainer se aplica, el contenedor cambia de ID y un archivo en `$HOME` sobrevive; de punta a punta por el hub, 202 → `running`.
10. ✅ **Variables y secretos por workspace.** Tabla `workspace_env` (migración 003), valores cifrados con AES-256-GCM; la clave vive en `<data>/secret.key` (0600, junto a `hub.db`: hay que respaldarla con la base). `GET/PUT /api/workspaces/{name}/env` (el PUT reemplaza todo el conjunto), `secret` solo cambia cómo lo muestra la UI. El agente recibe `env` al crear y al reconstruir y lo pasa con `podman run --env-file` desde un archivo temporal `0600` (los valores no aparecen en `argv`); en el rebuild el hub los reenvía porque el agente no los guarda. La UI tiene un editor por workspace y avisa de que se aplican al reconstruir. Probado: PUT/GET, validación, cascada al borrar, cifrado en la BD, y con podman real un `GREETING=hola` visible dentro del contenedor tras el rebuild.
11. ✅ **Servicios del nodo desde el workspace.** Verificado con podman real: con la red por defecto (pasta) el workspace **no** llega a puertos del host (probado `127.0.0.1`, la IP LAN y la de la tailnet, con el servicio escuchando en `0.0.0.0`; `--map-host-loopback` de pasta tampoco funcionó con este podman). Solución: red podman compartida **`walroos-dev`**. El agente la crea sola y arranca los workspaces en ella; un servicio del nodo que se una (`podman run --network walroos-dev`, o `networks: [walroos-dev]` external en compose) es alcanzable por **nombre de contenedor** desde el workspace, con DNS y salida a internet normales. Para DotnetApp: quitar `network_mode: pasta` del compose y unir el servicio a `walroos-dev`.
Notas de uso (para el README cuando haya flujo completo):
- No meter en un workspace credenciales de **producción**: el contenedor vive en un nodo del homelab y lo ve cualquiera con acceso al nodo. Para proyectos que trabajan contra la BD de producción (un proyecto .NET), usar una BD de desarrollo.
- Proyectos sin devcontainer arrancan en la imagen base y el runtime se instala con `mise use` (una vez, persiste en el home). Mejor añadir un `devcontainer.json` con la imagen del stack (p. ej. `mcr.microsoft.com/devcontainers/dotnet:10.0`) para que cada workspace nuevo nazca listo.

Decisiones abiertas de M1:
- Podman por CLI (como en M0) o por la API REST del socket. Default: seguir con CLI hasta que duela.

## Detección de stack ✅ (2026-11-18)

Plan y estado en [docs/devcontainer-detection.md](docs/devcontainer-detection.md). El agente detecta el stack con un clon superficial (`POST /v1/detect`), genera un `devcontainer.json` y la UI lo ajusta antes de crear; el JSON va en línea en el spec y el repo no se toca.

**Probado en real** (2026-11-18, Arch con internet, hub y nodo `build` por la tailnet): `POST /api/workspaces/detect` de droid en 1,2 s (android, `java:17`, `vscode`, SDK 37 / build-tools 36), workspace `droid-e2e` con ese devcontainer `running` en 40 s (imagen en caché), y `./gradlew assemblePublicDebug` → `BUILD SUCCESSFUL` en 5 min 24 s con el APK generado.

## M2 — MCP ✅ (2026-11-18)

`/mcp` en el hub (`internal/mcp`, `modelcontextprotocol/go-sdk` v1.8), streamable HTTP **stateless** con respuestas JSON: cada petición arma un servidor ligado al usuario de la petición (el owner al crear), sin sesiones que guardar. Va detrás del mismo middleware que la API (WhoIs + allowlist); sin API tokens: los clientes MCP ya están en la tailnet, los tokens llegan con M4 para CI. `GET`/`DELETE /mcp` responden 405 (sin eso caían en el `index.html` de la SPA).

Herramientas: `list_nodes`, `list_workspaces`, `detect_stack`, `create_workspace` (acepta el devcontainer de `detect_stack`), `get_workspace`, `start_workspace`, `stop_workspace`, `workspace_ssh_info` (bloque de `~/.ssh/config` y comando `ssh`, igual que `walroos ssh-config`). Los errores del servicio vuelven como error de herramienta legible (`isError`), no como fallo de protocolo. Sin `delete_workspace`/`rebuild` a propósito: destructivos, se hacen desde la UI.

Probado: test de integración con el cliente del SDK (SQLite real, router y auth reales, agente falso) y Claude Code 2.1 contra el hub dev (`claude -p --mcp-config`): lista nodos y workspaces, y el error "no online node" le llega legible.

**Probado de punta a punta por la tailnet** (2026-11-18, con NordVPN): hub en Windows, nodo `dev` en WSL (servicio de systemd), Claude Code en Windows llegando a la tailnet por un puente tsnet efímero. Claude Code detectó el stack, creó `mcpdemo` desde `octocat/Hello-World`, consultó `get_workspace` hasta `running` y pidió `workspace_ssh_info`; con ese comando, `ssh` entró como `dev` en `/workspaces/Hello-World` con el repo clonado.

**Probado desde una laptop con `tailscaled` en modo userspace** (2026-11-18): `claude mcp list` con `HTTP_PROXY=http://localhost:1055` → `walroos: √ Connected`; sin el proxy, "Failed to connect". Por el proxy con `curl`: `initialize`, las 15 herramientas y `list_nodes`. Sin probar una sesión completa con el modelo (el Claude Code de la app de escritorio no tenía login).

## Caminos tristes y seguridad (2026-11-18)

Atacado un hub y un nodo reales por la tailnet (API, MCP y SSH). Arreglado:
- **CSRF**: la API aceptaba JSON con `Content-Type: text/plain` desde cualquier página; una web visitada desde un dispositivo de la tailnet podía registrar su clave SSH o crear workspaces. Ahora todo el hub pasa por `http.CrossOriginProtection` (stdlib). `/mcp` ya estaba a salvo (exige `application/json`).
- **DNS rebinding**: se aceptaba cualquier `Host`, y la auth es por identidad de red. `whttp.AllowHosts`: solo el nombre del hub, su nombre MagicDNS, `localhost` e IPs (421 si no).
- **Symlinks fuera del repo**: `inside()` comparaba rutas como texto. Un repo con `ctx -> ../../../../../../..` como `build.context` copiaba el home del nodo (token del nodo, estado de tsnet) a la imagen; verificado con un canario. Ahora resuelve symlinks, y la lectura de `devcontainer.json` pasa por ahí.
- **`repo_url` local**: rutas y `file://` clonaban el disco del propio nodo. Solo URLs remotas (`https`, `http`, `ssh`, `git`, `user@host:path`).
- Cuerpos limitados a 4 MB.

Ya estaba bien (probado): `ext::` (git lo bloquea), `--upload-pack=` (va tras `--`), nombres con `;`, `../`, mayúsculas, mounts `bind` (también `type=volume,type=bind`), `dockerfile` con `../`, imagen inexistente, nombre duplicado, tipos y campos obligatorios del MCP, herramienta desconocida, clave SSH no registrada, cuerpo de 30 MB a `/mcp`.

Límites conocidos (sin arreglar):
- **Inyección de prompt**: lo que controla un repo (su `devcontainer.json`, errores de git) vuelve al cliente MCP como texto.
- **SSRF limitado**: el nodo clona cualquier URL de red, incluidas IPs internas y su propio `localhost` (solo protocolo git; los errores muestran el código HTTP).
- Sin cuotas: un usuario puede crear workspaces hasta llenar el nodo.
- Un `member` ve y para/arranca workspaces de otros por la API y MCP (por SSH no). En v1 no hay forma de crear members.
- Un devcontainer inválido se detecta al crear en el nodo (estado `failed`), no al llamar.

## M3.1 — Apps y deploy en un nodo ✅ (2026-11-18)

Una app es un repo con `Dockerfile` en un nodo `prod`. El hub (`internal/app`, migración `004_apps`) guarda la app, su env cifrado y el historial de deployments; una cola (una goroutine, un deploy a la vez en todo el hub) llama al agente y guarda el log que va llegando (NDJSON, últimos 1 MB). Al arrancar, los deployments que quedaron `running` pasan a `failed`.

En el nodo (`internal/agent/apps.go`):
- Clona (`--single-branch`, `sha` opcional), `podman build` a `localhost/walroos-app-<app>:<sha12>`.
- Cada release es un Quadlet `<app>-<deployment id>.container` en `~/.config/containers/systemd`, red `walroos`, sin puertos al host, env en un archivo `0600`. systemd lo reinicia, lo arranca al boot y sus logs van al journal.
- Healthcheck: `GET` al puerto desde el netns del contenedor con el propio binario (`walroos node relay`), sin `curl` en la imagen. 2xx/3xx en 90 s.
- Sin downtime: el nuevo arranca junto al viejo; si sana, se para el viejo; si no, se borra el nuevo y el viejo sigue sirviendo.
- Rollback: corre una imagen guardada, sin build. Se guardan las 3 últimas por app.
- `DefaultDependencies=false` en el Quadlet: si no, cada arranque espera `network-online.target`, que en WSL nunca llega (90 s por deploy).

API: `/api/apps` (crear, listar, ver, borrar), `/env`, `/deploy {sha}`, `/deployments`, `/deployments/{id}` (con log), `/deployments/{id}/rollback`, `/logs`.

Probado: test de integración con agente falso; test de podman en WSL (deploy, release rota que no reemplaza a la buena, deploy por sha, rollback, borrar); y **de punta a punta por la tailnet**: hub en Windows, nodo `dev,prod` en WSL, repo por `git daemon`: v1, v2, otro contenedor llega a la app por nombre en la red `walroos`, rollback a v1, logs y borrado limpio.

Caminos tristes (atacado el hub y el nodo reales por la tailnet). Arreglado:
- **Inyección de opciones en git**: `sha` llegaba a `git checkout` (`--orphan=x` pasó como opción; git lo frenó por suerte). Ahora `sha` es hex de 7 a 40.
- **`health_path` con CRLF**: se metía en la petición HTTP del healthcheck y falsificaba líneas del log (`health check: 200`). Ahora es una ruta URL sin espacios ni control.
- `branch` y `dockerfile` sin espacios ni `-` inicial; la imagen de un rollback tiene que ser un build de esa misma app.
- **Hub caído a mitad de deploy**: la limpieza usaba el contexto ya cancelado y la release nueva quedaba huérfana (con una vieja, dos corriendo). Ahora la limpieza no se cancela.
- **Delete durante un deploy**: si caía en el build, el deploy arrancaba la release después → app huérfana sin fila en el hub. Ahora deploy y delete toman el mismo lock por app (409).

Ya estaba bien: `Dockerfile` symlink fuera del repo, `dockerfile: ../../etc/passwd`, `COPY` por un symlink del contexto a `/home`, `--upload-pack` en `branch`.

Límite conocido: si el hub muere sin cerrar la conexión (tsnet no manda FIN), el nodo termina el deploy por su cuenta y el hub lo marca `failed` al arrancar. La release viva del nodo es la verdad; M3.3 la mostrará.

Falta: editar una app (puerto, rama) sin borrarla. Para pasar un nodo existente a `prod` hoy hay que volver a unirlo con `--roles dev,prod`.

## M3.3 — UI y MCP de apps ✅ (2026-11-18)

UI (`web/src/Apps.tsx`, portada del prototipo):
- **Apps**: tarjetas con el estado y el último deploy.
- **Detalle de una app**:
  - Deploy, con un SHA opcional.
  - Variables de entorno: el mismo `EnvModal` que los workspaces, ahora en `ui.tsx`.
  - Borrar.
  - Tabla de deployments: tocar una fila abre su log, en vivo mientras corre, y desde ahí se hace rollback.
  - Logs del contenedor cada 5 s.
  - Datos de build.
- **Deployments**: todos, de todas las apps.

El estado sale de la release que el nodo corre de verdad. `GET /api/apps` pregunta a cada agente una vez y agrega `running` y `node_down`. Así, un deploy que el hub marcó `failed` tras reiniciarse se ve corriendo igual. Los logs leen `CONTAINER_NAME=` del journal: solo la salida de la app, sin las líneas de systemd y podman.

MCP: `list_apps`, `get_app`, `create_app`, `deploy_app`, `get_deployment` (últimos 8000 caracteres del log), `rollback_app`, `app_logs`. Sin `delete_app` a propósito.

Probado:
- Hub real por la tailnet: crear, deploy, deploy fallido, rollback desde la UI y logs.
- Móvil (modales como bottom sheet, sin scroll horizontal), claro y oscuro, y Dracula.
- Herramientas MCP con `curl` contra el hub real.

Para iterar la UI contra otro hub: `WALROOS_API=http://127.0.0.1:9080 bun run dev`.

## M3.4 — Nodo build + registry ✅ (2026-11-18)

Si hay un nodo `build` online y el destino no tiene ese rol, el deploy va en dos fases. Si no, el destino construye como antes.
1. El hub llama a `POST /v1/apps/{name}/build` en el nodo build. Ese nodo clona, construye y hace `podman push` a su registry. No recibe las variables: son de la app corriendo.
2. El hub llama al deploy del nodo prod con `pull` y `pull_token`. El nodo prod hace el pull, retaguea a `localhost/walroos-app-<app>:<sha12>` y sigue igual: Quadlet, health check, swap.

Detalles:
- **Registry**:
  - Contenedor `walroos-registry` (`registry:2`) con red host, atado a `127.0.0.1:7075`, volumen `walroos-registry`.
  - Se arranca en el primer build. Red host porque el port forwarding rootless necesita NAT que WSL no tiene.
- **Tailnet**:
  - Cada agente escucha `:5000` en tsnet. Solo pasa GET/HEAD de `/v2/walroos-app-<app>/{manifests,blobs}/<ref>`.
  - Exige un token `HMAC(token_hash del nodo build, app)`. El hub y el nodo build lo calculan sin guardar nada. El token no llega al registry.
- **Pull en prod**: el podman del host no ve la tailnet (tsnet vive en el proceso). Por eso el agente abre un proxy efímero en `127.0.0.1:<random>`, que marca al nodo build por tsnet y pone el token. El tag `127.0.0.1:<port>/...` se borra después.
- **Rollback**: usa la imagen que ya está en prod, sin build ni pull.
- **Logs**: el log del deployment junta las dos fases ("building on X" … "deploying on Y").

Límites (`ponytail:`):
- El registry no hace GC: cada build suma solo sus capas propias.
- El token no expira, pero sirve solo para las imágenes de esa app.
- Con un nodo build caído se construye en el destino sin avisar.

Probado:
- Hub con agentes falsos: orden de las llamadas, sin env en el build, `pull`/`pull_token` correctos, rollback sin build, un build fallido no llega a prod, y un nodo `build,prod` construye para sí.
- Handler del registry: token de otra app, `tags/list`, `..`, `%2F` y PUT rechazados.
- Podman real en WSL: build, push a `registry:2`, `rmi` local, pull por el handler con token malo (falla) y bueno (live), sin tags sobrantes.
- **Dos nodos por la tailnet** (2026-11-18, un nodo `build` y uno `prod`, cada uno con su tsnet en la misma máquina y el mismo podman): build en el nodo `build`, push a su registry, el nodo `prod` hace pull de `<ip-del-build>:5000`, health 200 y sirve. Release rota: no pasa el health, se retira y la anterior sigue. Nodo build caído: se construye en el destino.
  - Mejora pendiente: con el nodo build caído el log no lo dice; solo falta la línea "building on X".
- **Registry con contraseña vieja** (2026-11-19, arreglado): si algo recrea `walroos-registry` con otra contraseña (otro agente en la misma máquina, una corrida de tests), cada push fallaba con "authentication required" hasta reiniciar el agente. Ahora `ensureRegistry` comprueba la contraseña con un `GET /v2/` autenticado y, si no sirve, recrea el registry. Probado: test de podman recrea el registry y el deploy siguiente se recupera solo.

## M3.2 — Dominios con Cloudflare Tunnel ✅ código (2026-11-19)

**Probado en Linux real** (2026-11-19, Arch, sin parches de NAT): token mínimo (Zone Read + DNS Write solo en la zona, Cloudflare Tunnel Write solo en la cuenta). Un dominio de prueba → 200 desde internet (5/5, ~0,35 s); deploy v7 → v8 con peticiones públicas cada 0,3 s: 44/44 OK, 0 errores. Dominio fuera de la zona → 400, de otra app → 409, registro A ajeno (el apex de la zona) → 409 y no se toca (antes respondía 500), borrar una app y quitar un dominio borran su CNAME y el ingress queda en 404.

**Setup:**
- En Settings → Cloudflare se pega un API token con Zone:Read, DNS:Edit y Account → Cloudflare Tunnel:Edit.
- Se valida listando zonas y se guarda cifrado en `settings` (migración 005).
- La cuenta sale de la zona. `ponytail:` una sola cuenta.

**Dominios:**
- Se editan en el detalle de la app, o con `PUT /api/apps/{name}/domains` o la herramienta MCP `set_app_domains`.
- La primera vez para un nodo, el hub crea (o reusa) el túnel `walroos-<nodo>` y le pasa el token al agente (`PUT /v1/tunnel`). El agente escribe el Quadlet `walroos-cloudflared` con el token en un env file `0600`, y lo reinicia solo si algo cambió.
- Después el hub reescribe el ingress del túnel con todos los dominios del nodo (`hostname → http://<app>:<port>`, más un 404 al final) y crea un CNAME proxied a `<tunnel>.cfargotunnel.com`.
- Si el registro ya existe y no es un CNAME a un túnel, falla en vez de pisarlo.
- Al quitar un dominio se borra su CNAME, si todavía apunta al túnel.
- Al borrar la app pasa lo mismo, como best effort.
- Un dominio pertenece a una sola app (PK).

**Zero-downtime sin tocar Cloudflare en cada deploy:**
- La release sana entra a la red `walroos-edge` con el alias `<app>`: `podman network connect` en caliente, y `Network=walroos-edge:alias=<app>` en su Quadlet para el próximo arranque.
- Hasta que se para la vieja, el alias resuelve a las dos, y las dos están sanas.
- Una release que no pasa el health check nunca entra a esa red.

**Probado:**
- Hub contra una API de Cloudflare falsa:
  - un token rechazado no se guarda, y el guardado está cifrado;
  - normalización de dominios;
  - túnel creado una sola vez y reusado;
  - ingress y CNAME;
  - dominios inválidos o fuera de zona → 400;
  - dominio de otra app → 409;
  - un registro A ajeno no se toca y los dominios vuelven atrás;
  - cambio de dominio borra el CNAME viejo;
  - borrar la app limpia el ingress y el DNS.
- Podman real en WSL (redes `--internal`):
  - la app responde por `http://<app>:8000` desde otro contenedor en `walroos-edge`;
  - el alias sigue tras `systemctl restart`;
  - el swap, el rollback y el build/pull siguen pasando.
- UI en móvil claro, escritorio oscuro y Dracula, sin scroll horizontal.

**Límites (`ponytail:`):**
- `cloudflared:latest`.
- Primera página de zonas (50).
- Si Cloudflare falla a mitad, los registros ya cambiados quedan, aunque los dominios en la base vuelven atrás.
- No hay forma de apagar el túnel de un nodo sin dominios.

**Probado de punta a punta (2026-11-19):** un dominio de prueba responde 200 desde un nodo en WSL. Para eso hizo falta un parche de NAT solo para WSL: el kernel no trae `nft_fib_inet`.
- Lo que salió de la prueba:
  - un token sin permiso de túnel ahora da un 502 claro, no un 500;
  - `cloudflared` reintenta cada 10 s (`RestartSec`), porque antes systemd se rendía tras 5 fallos rápidos.

## Apps desde una imagen publicada (2026-11-20)

Una app puede ser una imagen (`ghcr.io/owner/app:tag`) en vez de un repo: sin clone, sin build, sin nodo build. Primer paso para servicios como Immich.
- `apps.image` (migración 007). Repo **o** imagen, nunca los dos; con imagen no hay branch ni Dockerfile.
- La imagen tiene que venir con su registry (un host con punto): nada de nombres cortos ni `localhost/` (las builds de otras apps).
- Cada deploy hace `podman pull` en el nodo prod y etiqueta el id de la imagen como `localhost/walroos-app-<app>:<id12>`. El "sha" del deploy es ese id: el rollback corre esa imagen exacta aunque el tag se haya movido. Deploy con un sha → 400.
- UI: selector Repository / Published image en "New app"; la tarjeta "Build" pasa a "Source". MCP: `create_app` acepta `image`.
- Probado: test del hub (validación, sin build aunque haya nodo build, rollback) y podman real (`TestAppSourcePodman`: pull de `nginx:alpine`, redeploy, rollback).
- Límite (`ponytail:`): el tag original queda en el nodo (el dueño puede usarlo), así que borrar la app deja su imagen actual. Sin `command` por app: la imagen tiene que arrancar su servidor sola.

## Apps fuera de la LAN (2026-11-19)

Para el homelab: una app expuesta que cae no debe abrir la red de casa. Detalle en ARCHITECTURE.md, "Exponer apps desde un homelab".
- La regla nftables `inet walroos_guard` va en el netns rootless. Rechaza el tráfico de los bridges `walroos` y `walroos-edge` hacia redes privadas, link-local, CGNAT y tailnet.
  - Cada Quadlet de app y de `cloudflared` la vuelve a aplicar en `ExecStartPost`; si no se puede aplicar, la unidad falla.
  - Los workspaces quedan fuera de la regla.
- Las apps llevan `Memory=2g` y `PidsLimit=2048`.
- Límite (`ponytail:`): no hay excepciones por app para servicios de la LAN.
- **Probado en Linux real** (2026-11-19, Arch, Podman 6.2, cgroup v2 con `memory` y `pids` delegados):
  - Desde la app: router, tailnet (hub y laptop), NordVPN, `169.254.169.254` y `host.containers.internal` → "No route to host"; internet, DNS y app ↔ app funcionan; un workspace en `walroos-dev` sigue llegando a la LAN. Comparado con un contenedor sin la regla, que sí llega a la web del router y a un servicio del host por `host.containers.internal`. La IP LAN del host no es alcanzable desde ningún contenedor (pasta la hace local al netns).
  - Borrar la tabla a mano abre la LAN; reiniciar la unidad la vuelve a poner.
  - `Memory=2g`: un proceso que pide 3 GB llega a 1996 MiB y recibe "out of memory"; la app sigue sirviendo. `PidsLimit=2048`: "can't fork" a los 2047; el host no se entera.
  - **Arreglado:** si `nft` fallaba, systemd paraba el contenedor por las buenas y una app que ignora SIGTERM corría ~50 s sin la regla (y otra vez en cada ciclo de `Restart=always`). Ahora `ExecStartPost` hace `podman kill --signal KILL` si `nft` falla: el contenedor desaparece en 0,4 s. `ponytail:` entre el arranque y la regla (milisegundos, solo con el netns recién creado) la app no tiene regla.

## Auditoría de seguridad y caminos tristes (2026-11-18)

Revisión independiente (subagente sin contexto) con seis atacantes: otro equipo de la tailnet, una web maliciosa en el navegador del dueño, un repo malicioso, código dentro de un contenedor, un nodo comprometido, y entradas malas en API y MCP.

Arreglado:
- **Crítico: `mounts` del devcontainer.** Solo se miraba `type=`. `type=volume,volume-opt=device=/home/u,volume-opt=o=bind` montaba cualquier ruta del host (verificado: leer y escribir el `$HOME` del nodo, con su token y su estado de tsnet). Además se podía montar el volumen de otro workspace.
  - Ahora solo pasan las claves `type`, `source`/`src`, `target`/`dst`/`destination`, `ro`/`readonly`, `tmpfs-size` y `tmpfs-mode`. Sin comillas, y con un solo `type`.
  - En la forma objeto, las comas se rechazan.
  - Los volúmenes con nombre se llaman `ws-<workspace>-v-<source>`.
- **Alto: registry sin auth.** Un `RUN --network=host` en un Dockerfile llega al loopback del nodo build y podía leer o pisar imágenes del registry.
  - Ahora el registry exige htpasswd. La contraseña es aleatoria y vive solo en memoria del agente: si el agente reinicia, el registry se recrea en el siguiente build.
  - El push usa un `--authfile` temporal `0600`. El proxy de la tailnet pone su propio Basic auth.
- **Alto: `detect` con symlinks.** Un `go.mod -> /dev/zero` tumbaba el agente por memoria, y un FIFO lo colgaba. Ahora solo se leen archivos regulares, con 1 MB máximo.
- **Medio: memoria del agente.**
  - El log de build sin saltos de línea se corta en líneas de 64 KB.
  - El health check lee solo los primeros 4 KB de la respuesta.
- **Medio: workspace huérfano.** El nombre repetido se descubría al insertar, después de que el agente ya estaba creando el workspace, y un workspace a medio crear no se puede borrar. Ahora se revisa antes de llamar al agente.
- **Medio: `secret.key` perdida.** El hub creaba una clave nueva en silencio, y los deploys corrían sin secretos. Ahora no arranca si la base tiene variables cifradas y falta la clave.
- **Bajo:**
  - Tokens en URLs `https://user:token@…`: se ocultan en el log de deploy.
  - Aserción de tipo sin chequear en el SSH: podía tumbar el agente.
  - Directorio de datos `0700`.
  - Apps con `NoNewPrivileges=true`.
  - `GET /api/workspaces` ya no espera un timeout por cada fila de un nodo caído.

Ya estaba bien:
- Web del hub: `CrossOriginProtection`, `AllowHosts` contra DNS rebinding, sin CORS, cuerpos de 4 MB máximo.
- SQL parametrizado; join tokens de un solo uso.
- Agente: hub fijado por StableID y token comparado en tiempo constante.
- Proxy del registry, inyección en git, podman y systemd, symlinks del contexto de build, y canales del SSH.

Pendiente (`ponytail:`):
- **Builds:** pueden llegar a otros servicios en `127.0.0.1` del nodo build con `RUN --network=host` (podman no tiene forma de prohibirlo).
- **Aislamiento:** las apps se ven entre sí en `walroos` (ya no ven la LAN ni los workspaces; ver "Apps fuera de la LAN").
- **Cola de deploys:** un worker para todo el hub, así que un deploy colgado bloquea 30 min.
- **Log de deploy:** el append es lento con logs de más de 1 MB.
- **Gateway SSH:** sin límite de conexiones antes de autenticar.
- **MCP:** devuelve logs y errores al LLM sin sanear (riesgo de prompt injection).

## Después

| Hito | Resumen |
|---|---|
| M3 Deploy | Probar M3.2 en un nodo Linux real (homelab). |
| M4 CI/CD | Webhooks vía Funnel, `on_push` / `on_ci`, deploy tokens. |
| M5 | Previews por PR, compose, features de devcontainer, Caddy. |

## Desarrollo remoto real: laptop → PC (2026-11-19)

Hub en la laptop, nodo `dev` en una PC (Arch, Podman 6.2, con otra VPN en las dos). `walroos node join` + `node install` (servicio de systemd, linger) sin `tailscaled` en el nodo: el nodo va por su propio tsnet. Workspace `walroos` desde la detección de stack (`devcontainers/go:1.27`), listo en 49 s.
- Desde la laptop, `ssh walroos.walroos` (por el proxy userspace) entra como `vscode` en `/workspaces/walroos`; `go build` en 37 s y tests OK, con los 12 hilos del nodo.
- nvim 0.12.6 instalado en `~/.local` (volumen del home), PTY, `TERM` del cliente propagado, 256 colores, `gopls` en la imagen.
- Zed 1.29 con `zeditor ssh://walroos.walroos/workspaces/walroos`: instala su servidor en `~/.zed_server`, abre el repo y git, y corre el language server en el nodo (tras arreglar `sftp-server`, ver abajo).
- VS Code: sin probar (no está instalado en la laptop).
- **Hub mudado a la PC del nodo** (2026-11-19): parar el hub viejo, copiar `hub.db`, `secret.key` y `tsnet/` (misma identidad y misma IP de tailnet), arrancarlo allí. Los tres nodos se reconectaron solos en 5 s y el token de Cloudflare se descifró con la `secret.key` copiada. Falta en el producto:
  - [ ] `walroos hub install` (como `node install`): la unidad `walroos-hub.service` se escribió a mano.
  - [ ] Documentar la mudanza y el respaldo del hub (`hub.db` + `secret.key` + `tsnet/` van juntos; nunca dos hubs con la misma `tsnet/`).
- [ ] **El panel no se abre desde una laptop con `tailscaled` en modo userspace** (la de la otra VPN): el navegador no tiene ruta a la tailnet. Arreglo usado: un PAC que manda solo el hub, `*.ts.net` y `100.64.0.0/10` por el SOCKS `localhost:1055` (en Chromium, `--proxy-pac-url=data:…`). Mejor en el producto: `walroos ui`, que levante un tsnet efímero (el binario ya lo trae) y sirva el panel en `127.0.0.1:<puerto>`, con ese host permitido en `AllowHosts`.
- Repos privados: el nodo clona con el `git` y las llaves SSH del usuario del nodo (una llave sin passphrase). Funciona desde el servicio de systemd, sin agente SSH. Con una llave con passphrase no funcionaría: no hay credenciales de git en walroos.

## Casos tristes que walroos no detecta

- [ ] **Netns rootless sin salida, y el nodo reporta `online`** (encontrado 2026-11-19 en un nodo Arch con otra VPN).
  - Qué pasó: el proceso `pasta` del netns rootless murió (con 2 h de uptime; probablemente un cambio de red, como la VPN reconectando). El netns siguió existiendo sin interfaz de salida ni ruta por defecto. Todo contenedor en una red bridge se quedó sin internet ni DNS: el workspace (`go build` no bajaba módulos) y también un servicio ajeno a walroos.
  - Qué vio walroos: nada. El nodo seguía `online` con telemetría normal, el workspace `running` y el SSH funcionando. El primer síntoma fue un `lookup … i/o timeout` dentro del workspace.
  - Cómo se arregló: parar todos los contenedores del netns (podman lo destruye), arrancar uno (lo recrea con un `pasta` nuevo).
  - Qué haría walroos: el agente comprueba en cada heartbeat que el netns rootless tiene ruta por defecto y que hay un `pasta` vivo para él (o un `getent hosts` desde `podman unshare --rootless-netns`), lo manda en la telemetría y la UI marca el nodo como degradado con el arreglo en una línea. Repararlo solo (parar y arrancar contenedores ajenos) no: puede cortar servicios del dueño.
- [x] **Imágenes de devcontainer sin `sftp-server`** (encontrado 2026-11-19 con `devcontainers/go:1.27`; arreglado 2026-11-19). `scp`, `sftp` y las extensiones de Zed fallaban. Ahora, si la imagen no trae `sftp-server`, el gateway copia el binario del agente al contenedor una vez (`/usr/local/bin/.walroos-sftp-<hash>`; una versión nueva reemplaza la vieja) y corre `walroos sftp-server` (`pkg/sftp`) como el usuario del workspace. Sirve en workspaces ya creados, sin rebuild. Límite: un walroos con CGO necesita glibc; para imágenes musl (Alpine) compilar con `CGO_ENABLED=0`, y el subsistema lo explica si no puede correr.
- [x] **Zed (y VS Code) abrían `$HOME`** en vez del repo (arreglado 2026-11-19): el hub devuelve `folder` (la carpeta real, viva desde el nodo) y la tarjeta Connect da `zed ssh://<ws>.walroos/workspaces/<repo>` y `code --remote ssh-remote+<ws>.walroos /workspaces/<repo>`. Para la UI de Zed (*Open Remote*), `walroos ssh-config --zed --write` escribe en su `settings.json` una entrada por workspace con su carpeta como proyecto (JSONC: comentarios y resto intactos, symlinks seguidos, duplicados fusionados, entradas de workspaces borrados quitadas, idempotente; un archivo inválido no se toca). La opción "Connect Dev Container" de Zed 1.29 es de Zed (Docker local), no de walroos.
- [ ] **Terminal sin terminfo** (Ghostty manda `TERM=xterm-ghostty`; kitty, `xterm-kitty`): las imágenes no traen esas entradas y `nvim`, `clear` o `tput` fallan con "unknown terminal type". Arreglo por workspace, persistente en el volumen del home: `infocmp -x xterm-ghostty | ssh <ws>.walroos -- tic -x -`. Opciones: que `walroos ssh-config` lo documente o lo haga, `shell-integration-features = ssh-terminfo` en Ghostty, o instalar `ncurses-term` en la imagen base.
- [ ] **Sin identidad de git en el workspace**: `git commit` falla hasta configurar `user.name` y `user.email`. Opción: que el hub los guarde por usuario y el agente los pase como `GIT_AUTHOR_*`/`GIT_COMMITTER_*` (o los escriba en `~/.gitconfig` del volumen del home al crear).
- [ ] **Un intento de `ssh` por el proxy userspace falló** con `SOCKS server failure` justo después de reiniciar el workspace; el reintento funcionó. Ver si se repite antes de tocar nada.

## Backlog chico

- Self-host de las fuentes (hoy Google Fonts; offline cae a fuentes del sistema).
- Pantallas placeholder del sidebar (Overview, Apps, …) se rellenan con su hito.
