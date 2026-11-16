# Walroos — Brief para Open Design

**Cómo usarlo**
1. En Open Design: modo **prototype (web)**, plugin **`dashboard`**.
2. Elige un design system tipo herramienta para devs: Linear, Vercel/Geist, Raycast o el más parecido que ofrezca. Si no te convence ninguno, usa la sección *Dirección visual* de abajo.
3. Pega primero el **Brief global** y luego **una pantalla por prompt** (P1…P9). Cada pantalla es un artifact aparte; así se iteran por separado.
4. Al final, exporta con `od-react-export` o pásale el HTML a Claude Code para convertirlo a React + Vite + TS + Tailwind.

Contexto técnico completo: [ARCHITECTURE.md](ARCHITECTURE.md).

---

## Brief global (pegar siempre primero)

> **Producto:** Walroos, una plataforma self-hosted para desarrolladores indie que corre en su homelab o VPS. Hace dos cosas:
> 1. **Workspaces**: entornos de desarrollo remotos en contenedores Podman. El dev se conecta por SSH desde su laptop, VS Code o Claude Code, sin instalar nada localmente.
> 2. **Apps**: deploy de repos Git a producción, estilo Dokploy o Vercel self-hosted, con auto-deploy al hacer merge a `main`.
>
> Hay varios **nodos** (máquinas) unidos por una red privada Tailscale, cada uno con roles: `dev`, `build`, `prod`.
>
> **Usuario:** un desarrollador full-stack, técnico, que vive en la terminal. Quiere ver el estado de un vistazo y llegar a cualquier acción en ≤2 clics. Odia los wizards largos y el marketing dentro de la app.
>
> **Tono:** herramienta de ingeniería seria y densa en información, pero tranquila. Referencias: Linear, Vercel dashboard, Railway, Fly.io, Tailscale admin. Nada de gradientes chillones ni ilustraciones de stock.
>
> **Layout base:**
> - Sidebar izquierda fija (240px, colapsable a iconos) con logo "walroos", un selector de nodo/entorno arriba y navegación: Overview, Workspaces, Apps, Deployments, Nodes, Integrations, Settings. Abajo: usuario (identidad de Tailscale, p. ej. `jefjesuswt@github`), estado de conexión y versión.
> - Topbar con breadcrumb, buscador/command palette (`⌘K`) y un botón primario contextual.
> - Contenido con ancho máximo de 1280px.
>
> **Requisitos:** dark mode por defecto y light mode completo; responsive hasta 375px (la sidebar pasa a drawer); estados vacío, cargando y error en cada lista; monospace para IDs, SHAs, comandos, logs y nombres de host; todo comando copiable con botón de copiar.
>
> **Estados con color consistente:**
> - `running` / `succeeded` / `online` → verde
> - `building` / `deploying` / `starting` → ámbar, con animación sutil
> - `failed` / `offline` → rojo
> - `stopped` / `queued` → gris
>
> **Idioma de la UI:** inglés.

---

## Dirección visual (si no usas un design system existente)

- **Concepto:** "ártico". El nombre es un juego con *walrus* (morsa): hielo, agua fría y calma.
- **Paleta dark:**
  - Fondo `#0B0F14`, superficies `#111820` / `#17202A`, bordes `#1F2A36`.
  - Texto `#E6EDF3` / `#8B98A5`.
  - Acento cian hielo `#5CC8E6`.
  - Estados: verde `#3FB950`, ámbar `#D29922`, rojo `#F85149`.
- **Tipografía:** Inter (UI) + JetBrains Mono (código y datos), base 14px, densidad compacta.
- **Formas y movimiento:** radios de 6–8px, sombras casi nulas (la jerarquía la dan bordes y superficies) y animaciones ≤150ms.
- **Logo:** morsa minimalista de línea o una "w" con colmillos. Monocromo, para que funcione a 16px.

---

## Datos de ejemplo (usar en todas las pantallas)

**Nodos**

| name | roles | status | CPU | RAM | containers | tailnet host |
|---|---|---|---|---|---|---|
| `homelab-1` | dev, build | online | 23% | 9.1 / 32 GB | 6 | `homelab-1.tail3f2a.ts.net` |
| `homelab-2` | prod | online | 41% | 5.4 / 16 GB | 4 | `homelab-2.tail3f2a.ts.net` |
| `vps-fra` | prod | offline (last seen 2h ago) | — | — | — | `vps-fra.tail3f2a.ts.net` |

**Workspaces**

| name | repo | branch | node | status | detail |
|---|---|---|---|---|---|
| `walroos` | `github.com/jefjesuswt/walroos` | `main` | homelab-1 | running | Go 1.26 + Node 22 |
| `tienda-api` | `forgejo.home/jef/tienda-api` | `feat/pagos` | homelab-1 | running | Bun 1.3 |
| `blog` | `github.com/jefjesuswt/blog` | `main` | homelab-1 | stopped | última actividad hace 3 días |

**Apps**

| name | repo | node | domain | trigger | last deploy |
|---|---|---|---|---|---|
| `tienda-api` | `forgejo.home/jef/tienda-api` | homelab-2 | `api.tienda.walroos.com` | on_ci | `a1b2c3d` "fix: idempotencia en pagos", succeeded, hace 12 min |
| `blog` | `github.com/jefjesuswt/blog` | homelab-2 | `blog.walroos.com` | on_push | succeeded, hace 2 días |
| `landing` | `github.com/jefjesuswt/landing` | vps-fra | `walroos.com` | manual | failed: "healthcheck timeout on :3000" |

---

## Pantallas

### P1 — Overview
> Pantalla de inicio. Fila de 4 stat tiles: Nodes online (2/3), Workspaces running (2), Apps healthy (2/3), Deploys today (5).
>
> Debajo, en dos columnas:
> - **Izquierda:** "Recent deployments", timeline con estado, app, SHA corto, mensaje del commit, trigger (webhook / manual / CI / MCP) y duración.
> - **Derecha:** "Nodes", tarjetas compactas con barras de CPU y RAM; el nodo offline se resalta en rojo con "last seen".
>
> Banner discreto arriba si algo falló: "landing deploy failed — healthcheck timeout" con acciones "View logs" y "Rollback".

### P2 — Workspaces (lista)
> Tabla/lista de workspaces con nombre, repo (icono GitHub, GitLab o Forgejo), branch, nodo, estado y "last active". Acciones por fila en hover: Connect, Start/Stop, menú (Rebuild, Delete).
>
> Botón primario "New workspace". Estado vacío con una ilustración mínima y el texto "Run your dev environment on your own hardware. No local installs."

### P3 — New workspace (modal o panel lateral, no wizard)
> Un solo formulario con:
> - Repo URL, con autocompletado desde las integraciones conectadas.
> - Branch.
> - Node: select solo con nodos de rol `dev`, mostrando la carga actual.
> - Configuración: "Detected .devcontainer/devcontainer.json ✓" o un fallback "Walroos base image (Debian + mise)".
> - Recursos opcionales, colapsados: CPU/RAM limit.
>
> Tras crear, se ve el progreso inline por pasos (clone → pull/build image → start → ready) con logs colapsables.

### P4 — Workspace detalle
> Cabecera: nombre, estado, nodo, repo@branch, uptime y botones Stop / Rebuild / Delete.
>
> Pestañas:
> - **Connect** (la principal), con tarjetas para cada método y snippets copiables:
>   - **SSH**: `ssh walroos.walroos`
>   - **VS Code**: botón "Open in VS Code" (`vscode://vscode-remote/ssh-remote+walroos.walroos/workspaces/walroos`)
>   - **Claude Code**: "Add SSH connection → walroos.walroos", o `ssh walroos.walroos -t claude`
>   - **SSH config**: un bloque para pegar en `~/.ssh/config`, o `walroos ssh-config >> ~/.ssh/config`
> - **Terminal**: terminal web embebida (xterm.js) a pantalla completa.
> - **Ports**: puertos detectados (3000, 5173), cada uno con el comando de port-forward.
> - **Logs**.
> - **Settings**: env vars, recursos y volúmenes (src, home) con su tamaño.

### P5 — Apps (lista)
> Grid de tarjetas, una por app: nombre, dominio (link), nodo, badge de trigger (`on push` / `on CI` / `manual`), último deploy (estado, SHA, mensaje, tiempo relativo) y un sparkline de deploys de los últimos 14 días.
>
> Botón primario "New app".

### P6 — App detalle
> Cabecera: nombre, dominio con candado TLS, estado del contenedor y los botones "Deploy now" (primario, con selector de branch/SHA) y "Rollback".
>
> Pestañas:
> - **Deployments**: historial con estado, SHA, mensaje, autor, trigger, duración e imagen `walroos/tienda-api:a1b2c3d`. La versión activa lleva el badge "current". Cada fila tiene "Rollback to this".
> - **Logs**: runtime logs en vivo, con filtro y pausa.
> - **Environment**: tabla key/value; los secretos van enmascarados con un botón de revelar. Incluye importar desde `.env`.
> - **Domains**: lista de dominios, ingress (Cloudflare Tunnel / Tailnet only) y estado del DNS.
> - **Build**: Dockerfile path, puerto interno y healthcheck path.
> - **Triggers**: elegir on push / on CI / manual. En "on CI" se muestra el snippet de GitHub Actions copiable con el deploy token.

### P7 — Deployment detalle (vista de log)
> Vista de un deploy en curso o terminado. Stepper horizontal: Queued → Clone → Build → Release → Route → Healthcheck, con la duración de cada paso.
>
> Debajo, un log en streaming en monospace, agrupado por paso y colapsable, con timestamps, autoscroll y búsqueda.
>
> Si falla: el error destacado arriba ("Healthcheck failed: GET :3000/health timed out after 30s"), un aviso "Previous version still serving" y los botones "Retry" y "View previous".

### P8 — Nodes
> Lista de nodos con roles (chips), estado, CPU/RAM/disco, versión de Podman y walroos, número de contenedores y "last seen".
>
> El detalle de un nodo muestra gráficas de 24h y la lista de contenedores con sus apps y workspaces.
>
> Botón "Add node": modal con un único comando copiable que incluye el join token (`curl -fsSL get.walroos.com | sh -s -- node join --hub walroos-hub --token wlr_join_…`). Queda esperando ("Waiting for node to connect…") y pasa a verde cuando el nodo aparece.

### P9 — Integrations y Settings
> **Integrations:**
> - Tarjetas para GitHub, GitLab, Forgejo/Gitea (connect con token o URL) y Cloudflare (API token y zona `walroos.com`; muestra el túnel por nodo).
> - Sección "Webhooks": URL pública (`https://walroos-hub.tail3f2a.ts.net/hooks/github`) y las últimas entregas con su estado.
>
> **Settings:**
> - Profile: identidad de Tailscale.
> - SSH keys: lista, más "Add key".
> - API tokens: nombre, scopes y último uso. "Create token" muestra el token una sola vez.
> - MCP: la URL `/mcp` y el snippet para añadirlo a Claude Code (`claude mcp add walroos --transport http https://walroos-hub.tail3f2a.ts.net/mcp --header "Authorization: Bearer …"`).

---

## Extra opcional

- **Command palette (`⌘K`)**: buscar workspaces, apps y nodos, y acciones rápidas ("Deploy tienda-api", "Connect to walroos", "Add node").
- **Pantalla de primer arranque**: cero nodos y cero apps; tres pasos con check: Add a node → Connect Git → Create your first workspace.
