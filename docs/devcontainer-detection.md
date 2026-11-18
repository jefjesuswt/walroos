# Detección de stack y devcontainer generado

Plan de la siguiente tanda. Objetivo: al crear un workspace, walroos inspecciona el repo y propone un `devcontainer.json` (imagen + herramientas) que el usuario puede ajustar en la UI, sin modificar el proyecto y sin que mise sea obligatorio.

Estado (2026-11-18): **implementado** (pasos 1–4), falta la prueba real de droid con podman e internet (paso 5).

- Agente: `devcontainer` en línea en el spec y `POST /v1/detect` (`internal/agent/detect.go`). Probado con podman real: el JSON en línea gana al del repo, `git status` queda limpio y el rebuild lo conserva.
- Hub: `POST /api/workspaces/detect` y `CreateInput.devcontainer`.
- UI: botón **Detect** en New workspace, herramientas con su origen (`in image` bloqueada, `postCreate` se puede quitar) y el `devcontainer.json` editable.
- Detección probada contra los repos del escritorio: tres repos Android → android (JDK 17, `platforms;android-37.0`, build-tools 36.0.0); tres repos .NET → dotnet 10.0; walroos → go 1.27 con nota de `web/package.json`. Contra GitHub (droid) tarda ~1,5 s.
- Cambios respecto al plan: los marcadores de la raíz ganan a los de subcarpetas (si no, walroos salía como bun); un segundo stack solo deja una nota; se añadió java/gradle sin Android.

---

## Decisiones

### El devcontainer generado no vive en el repo
El spec del agente gana un campo **`devcontainer`** (JSON en línea). Si viene, tiene prioridad sobre `.devcontainer/devcontainer.json` y se parsea sin escribir nada en el clon:

- `git status` queda limpio y el proyecto no se modifica.
- Se guarda en `$HOME/.walroos/workspace.json` junto al resto del spec, así el rebuild usa el mismo.
- Alternativa descartada: escribir el fichero en el clon y excluirlo en `.git/info/exclude`. Se ve sucio y se pierde al re-clonar.

### El origen de cada herramienta es explícito
Cada herramienta propuesta dice de dónde sale: `image` (ya viene en la imagen) o `postCreate` (se instala). La UI las muestra así y las de `image` quedan bloqueadas. Mise solo aparece cuando la imagen no trae la herramienta.

### Sin features (por ahora)
`devcontainer features` son artefactos OCI que instala `@devcontainers/cli`. Quedan fuera: imagen + `postCreate` cubren casi todo. Se añadirán cuando duela.

---

## Detección

`POST /v1/detect` en el agente, con `{repo_url, branch}`:

1. Clon superficial (`git clone --depth 1`) a un directorio temporal.
2. Escanea los ficheros y borra el temporal.
3. Responde:

```json
{
  "stack": "android",
  "image": "mcr.microsoft.com/devcontainers/java:1-17-bookworm",
  "remote_user": "vscode",
  "tools": [
    { "id": "jdk", "label": "JDK", "version": "17", "source": "image" },
    { "id": "android-sdk", "label": "Android SDK", "version": "37.0", "source": "postCreate" }
  ],
  "devcontainer": { "...": "..." },
  "notes": ["compileSdk 37 · build-tools 37.0.0"]
}
```

### Reglas

| Detecta | Propone |
|---|---|
| `bun.lock` o `bun.lockb` | imagen node + bun |
| `package-lock.json` / `pnpm-lock.yaml` | node (npm / pnpm) |
| `*.csproj` o `global.json` | `devcontainers/dotnet:<versión>` |
| `build.gradle.kts` + `AndroidManifest.xml` | `devcontainers/java:17` + SDK de Android en `postCreate` |
| `build.gradle.kts` sin manifest | java + gradle |
| `go.mod` / `Cargo.toml` | go / rust |
| `pyproject.toml` / `requirements.txt` (+ `uv.lock`) | python (+ uv) |
| `docker-compose.yml` | nota: servicios aparte, unirlos a `walroos-dev` |
| nada de lo anterior | imagen base de walroos |

La receta de Android es la que ya está probada en droid:

- imagen `mcr.microsoft.com/devcontainers/java:1-17-bookworm`;
- `postCreateCommand` que instala `cmdline-tools`, `platform-tools`, `platforms;android-37.0` y `build-tools;37.0.0`;
- `ANDROID_HOME` / `ANDROID_SDK_ROOT` en `containerEnv`.

Ojo con dos detalles aprendidos ahí:

- el SDK usa **versiones menores**: el paquete es `platforms;android-37.0`, no `android-37`;
- en redes pasta el DNS puede resolver por IPv6 y Gradle cae con `Temporary failure in name resolution`; si se repite, `JAVA_TOOL_OPTIONS=-Djava.net.preferIPv4Stack=true`.

---

## Hub

`POST /api/workspaces/detect` con `{repo_url, branch, node?}`:

- elige nodo con el mismo criterio que crear (dev online, menos cargado);
- llama al agente y devuelve su respuesta tal cual;
- no guarda estado: es una consulta.

`CreateInput` gana `devcontainer` (JSON) y el servicio lo reenvía al agente al crear y al reconstruir. El agente ya lo persiste en el spec del volumen, así que no hace falta migración.

---

## UI

En **New workspace**:

- botón **Detect** junto a Repository; rellena nombre sugerido, rama, stack y herramientas;
- **selector de herramientas** por categoría, con origen visible (`image` / `postCreate`);
- **Preview** del `devcontainer.json` generado, con opción de editarlo a mano;
- todo con los componentes existentes: `Field`, `Modal`, `Cmd`, y `Confirm` para acciones.

Riesgo de UX: la detección tarda lo que un clon superficial (segundos); el botón muestra un spinner y se puede crear sin detectar.

---

## Tests

- **Unitarios de detección** con repos de fixture por stack, lockfiles alternativos, repo sin stack y repo inaccesible.
- **Hub**: `detect` con agente falso (200, 404, 502) y `CreateInput.devcontainer` llegando al agente.
- **Podman real**: crear con devcontainer en línea → imagen y usuario correctos, `postCreate` ejecutado, **repo limpio** (`git status` vacío), y rebuild conservando el JSON.
- **Caso real**: el generador produce el devcontainer de droid y `./gradlew assemblePublicDebug` compila.

---

## Orden de trabajo

1. `devcontainer` en línea en el spec + tests con podman.
2. Detección en el agente + fixtures.
3. Endpoint del hub + tests.
4. UI (detect, picker, preview).
5. Docs y prueba real contra droid.

## Fuera del plan

- Guardar el JSON generado en la base de datos (el agente ya lo conserva en el volumen).
- `devcontainer features` y `docker compose`.
