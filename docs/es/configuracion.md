> 🌐 **Idioma / Language:** **Español** · [English](../en/configuration.md)

# Configuración

## `bflow.yaml` y sus capas

Hay tres capas; la última gana:

1. **Global** (`~/.config/bflow/config.yaml`, en Windows `%AppData%\bflow\config.yaml`), con perfiles compartidos entre repos.
2. **Perfil**, el que cada repo referencia.
3. **Repo** (`bflow.yaml`), solo con lo propio de ese repo.

`bflow init` crea el `bflow.yaml`: detecta stack, remoto, rama base y propone los pasos de check. Pregunta solo lo que no pudo detectar; con `--yes` no pregunta nada. Sin `bflow.yaml`, bflow funciona con el tracker local y solo con git.

```bash
bflow init [--yes] [--profile p] [--tracker local|plane|github] [--project P]
bflow doctor       # valida config, herramientas, conexiones y hooks
```

### Perfiles

```bash
bflow profile add acme --tracker plane --url https://plane.example.com --host github --base dev
bflow profile list
bflow profile use acme       # en este repo
```

```yaml
# global
profiles:
  acme:
    tracker: { adapter: plane, url: https://plane.example.com, workspace: acme }
    vcs: { host: github, base_branch: dev }
    agent: claude
```

```yaml
# bflow.yaml del repo (lo genera bflow init)
profile: acme
stack: go
tracker: { project: API }        # identificador legible, nunca UUID
check:
  steps:
    - { name: vet,   run: "go vet ./..." }
    - { name: test,  run: "go test -count=1 {race} ./..." }
    - { name: lint,  run: "golangci-lint run --new-from-rev={base}", needs: golangci-lint }
    - { name: vulns, run: "govulncheck ./...", needs: govulncheck, optional: true }
```

### Validación

La validación junta todos los problemas en un solo mensaje, indica la línea de los campos desconocidos y rechaza cualquier clave que parezca un secreto.

## Check

`bflow check [ID]` corre los pasos de `check.steps`, resume los fallos en pocas líneas y liga el resultado a un commit; el detalle completo queda en un archivo (`bflow show <ID> check`).

- `--quick <paquete>`: solo los pasos rápidos sobre un paquete, para iterar. No registra resultado, así que no cuenta para reportar DONE.
- `check.test_step` (por defecto `test`): el paso cuyo fallo cuenta como reproducción de un hotfix. Cada `bflow check` con ID de tarea guarda su resultado también por commit en `.bflow/tasks/<ID>/checks/<sha>.json` (no con cambios sin commitear).
- `--verify`: no corre los pasos; comprueba que el último check pasó y corresponde al código actual (y que las pruebas congeladas no cambiaron).
- Un paso con `needs` depende de una herramienta: si falta y el paso es `optional: true`, se omite con aviso; si no es opcional, falla.
- Un paso de vulnerabilidades marca como preexistentes las que la tarea no tocó (ver la [guía](guia.md#cuando-algo-se-atora)).

## Conectar un tracker

Los tokens se validan antes de guardarse en el llavero del sistema (Credential Manager, Keychain o Secret Service) y nunca se escriben en YAML. En CI se usan variables de entorno: `BFLOW_PLANE_TOKEN`, `GH_TOKEN`.

```bash
bflow connect plane --url https://plane.example.com --workspace mi-workspace
bflow connect github --from-gh      # o --token
bflow tracker states                # cómo se traducen las fases a tus estados (solo lectura)
bflow tracker setup --dry-run       # qué estados faltarían crear
bflow tracker setup                 # crea los que falten
```

### Tracker local

Es el predeterminado: las tareas son archivos en el repo, sin cuenta ni token. `bflow task add "título"` crea `LOCAL-1`, y `bflow task list` lista las abiertas.

### Plane

`tracker.adapter: plane` con la URL y el workspace (del perfil o de `bflow.yaml`). Las fases se traducen a los estados del proyecto con nombres literales; `bflow tracker states` los muestra y `bflow tracker setup` crea los que falten. Las tareas nuevas nacen en el estado de la fase backlog; si el proyecto no lo tiene, `bflow tracker setup` lo crea.

### GitHub Issues y Projects

Las tareas pueden vivir en los issues del repo (`GH-42` es el issue #42), con o sin un Project v2. Exige `vcs.host: github`; de ahí salen el repo, la API (`vcs.api_url`) y el token de `bflow connect github`.

```yaml
vcs: { host: github }
tracker:
  adapter: github
  project: mi-org/7        # opcional; sin él, la fase es una etiqueta bflow:<fase>
  # prefix: GH             # prefijo de los IDs (GH por defecto)
  # start_field: Start date  # campo de fecha del project donde se sella el inicio
```

- **Sin project:** la fase es la etiqueta `bflow:<fase>`. `bflow tracker setup` crea las que falten. Un issue abierto sin etiqueta es backlog; uno cerrado, done. No hay fecha de inicio.
- **Con project:** la fase es una opción del campo de selección única Status, con los mismos nombres que Plane (`tracker.states` los cambia). `bflow tracker setup` agrega las opciones que falten si la API conserva los ids de las existentes; si no, no escribe nada y lista qué crear a mano (`crea a mano en el project mi-org/7, campo Status:`). Al empezar, el issue entra al project y se sella la fecha de inicio si existe el campo.
- Al llegar a done se cierra el issue (bflow nunca reabre uno cerrado) y el PR lleva `Closes #N`.
- **Token:** el mismo de `bflow connect github`. Para Projects de una organización hace falta un token fine-grained con el permiso de organización Projects; para Projects de un usuario, un token clásico con el scope `project`. `bflow doctor` lo revisa.
- `bflow init` ofrece `github` cuando el remoto es de GitHub y deja elegir el project (o ninguno): `bflow init --tracker github --project owner/N`.

## Agentes

`agent:` acepta una herramienta (`agent: claude`) o una lista (`agent: [claude, opencode]`); vacío equivale a `claude`. `bflow init --agent claude,opencode` acepta varias, y detecta `.opencode/`, `opencode.json` y `opencode.jsonc`.

```bash
bflow render           # escribe .claude/agents/bflow-<agente>.md; commitéalos
bflow render --check   # en CI: falla si no coinciden con la configuración
```

Cada agente tiene dos capas. El **contrato** con bflow (qué recibe, qué archivos escribe, qué veredictos puede reportar y cómo) sale del flujo y no se configura. El **oficio** (cómo hace su trabajo) trae un default y cada repo lo complementa:

```yaml
# bflow.yaml
agents:
  implementer:
    model: sonnet
    effort: medium
    read: [docs/architecture/]           # el agente los lee antes de empezar
    extra: docs/bflow/implementer.md     # instrucciones propias, se copian al agente
  documenter: { model: haiku, effort: low }
```

Con `read`, el agente no carga CLAUDE.md (`omit_claude_md: false` lo cambia): las reglas del repo le llegan por esas rutas. Un agente propio se agrega a una fase en `flow.agents` y define su oficio en `extra`; bflow le antepone el contrato. Los agentes generados llevan el prefijo `bflow-` para no chocar con los tuyos; `render` nunca pisa un archivo que no generó. Si el repo tiene `AGENTS.md` (lo leen Codex y OpenCode), `render` mantiene en él un bloque que dice cómo retomar una tarea con bflow. `bflow doctor` avisa si los agentes están desactualizados y si alguna skill instalada parece de proceso (ramas, PR, tracker, specs), porque puede chocar con el flujo; `doctor.ignore_skills` silencia las que no chocan. Más ejemplos de oficio en la [guía](guia.md#ajustar-a-los-agentes).

### Salida pública y changelog

`flow.public_paths` lista las rutas del repo que cambian lo que ve quien usa el producto (comandos, JSON, panel, config). Si el diff las toca y el changelog para consumidores no cambió, bflow rechaza el DONE del documenter con `changelog_required`. Sin la clave, el gate no actúa.

```yaml
# bflow.yaml
flow:
  public_paths: [cmd/, internal/cli/, internal/config/config.go, "!*_test.go"]
```

Una entrada que termina en `/` cubre la carpeta; las demás se comparan con `path.Match` (un patrón sin `/` también contra el nombre base); `!` al inicio excluye. Deben ser rutas del repo: sin `..`, absolutas ni `.bflow/`. Para eximir una tarea: `bflow unblock <ID> --no-changelog --note "motivo"`.

### Comentarios que narran la tarea

`report DONE` del implementer y del documenter, y `bflow check --verify`, miran las líneas de comentario que el diff contra la base agrega dentro de `check.code_paths` (sin los `.md`). Si alguna cita el ID de la tarea, `R<n>`, `T<n>`, `spec`, `según`, `decisión` o `walkthrough`, el DONE sale con código 2 y `narrative_comment`, y `--verify` con `not_verified`. Un comentario es una línea que empieza con `//`, `#`, `/*`, `*`, `-- ` o `<!--`, o que lleva un `//` final fuera de comillas (`"`, `'` y backticks de la misma línea). Las directivas pegadas al `#` (`#include`, `#define`, `#pragma`…) no cuentan. No hay parser por lenguaje.

- `flow.comment_patterns`: lista de expresiones regulares que reemplaza a los patrones fijos. El ID de la tarea se busca siempre.
- `flow.narrative_comments: false`: apaga la regla. Una lista vacía en `comment_patterns` no la apaga: vuelve a los patrones fijos.

```yaml
# bflow.yaml
flow:
  comment_patterns: ['\bR\d+\b', '\bT\d+\b', '(?i)\bwalkthrough\b']  # sin "spec", por los YAML de OpenAPI
  # narrative_comments: false                                                    # o apaga la regla
```

### Presupuesto de tokens y detección de ciclos

`flow.budget` fija un presupuesto de tokens nuevos por fase. Cuando una fase lo supera, `bflow watch` lo marca en ámbar y el siguiente comando (`next`, `report`, etc.) es una pregunta al gate `budget`: "¿Sigue?", "Sigue" continúa, "Bloquear" bloquea con una nota que el implementer verá. No corta el trabajo, solo pide confirmación.

```yaml
flow:
  budget:
    implementing: 200k          # 200,000 tokens nuevos
    quality: 1.5M               # 1,500,000 tokens nuevos
    # La cifra puede ser un entero (200000) o con sufijo k/K (×1e3) o m/M (×1e6).
```

Una fase sin presupuesto en `flow.budget` no genera pregunta. Hasta 1×, 2×, 3× el presupuesto no aparece el gate. Después de responder "No" en un múltiplo, el siguiente múltiplo sí pregunta de nuevo.

`flow.loop_repeats` detecta cuando un subagente repite la misma acción (Bash, Read, Edit, Write con la misma entrada). A la N-ésima repetición idéntica, el guard la rechaza y `bflow watch` muestra el ciclo detectado. Un Edit o Write permitido reinicia la cuenta de Bash y Read (para no frenar relanzamientos de pruebas tras un cambio). `0` apaga la detección; `2` frena a la segunda repetición.

```yaml
flow:
  loop_repeats: 2              # Frena a la segunda repetición idéntica (0 = apagado).
```

## Claude Code y OpenCode

### Claude Code

`bflow install claude` instala la skill y los hooks (ver [instalación](instalacion.md#instalar-en-tu-herramienta-de-agente)). La skill tiene unas 30 líneas: no contiene reglas del flujo, solo cómo interpretar `next`. Los hooks corren:

- `bflow hook session-start` al abrir la sesión.
- `bflow guard --reads` antes de cada edición, comando o `Read` (de los `Read` solo registra los del reviewer en quality, para medir cuánto del diff abrió).
- `bflow hook tokens` al terminar cada turno y cada subagente (cada uno cuenta solo su transcript).
- `bflow hook subagent-stop` cuando termina un agente de bflow: si no reportó, lo hace seguir hasta 2 veces con lo que le falta y después bloquea la tarea para que decida una persona.
- `bflow statusline`, la barra de estado:

```
API-12 · implementing · 1h42m · ronda 1 · 145.1k nuevos · 2.9M caché
```

Los archivos fuente están en [`adapters/claude/`](../../adapters/claude/).

### OpenCode

Con `opencode` en `agent:`, `bflow render` escribe `.opencode/agents/bflow-<agente>.md` (subagentes con permisos `edit` y `bash` según lo que hace cada agente) y `bflow render --check` cubre los dos conjuntos. También escribe `.opencode/plugins/bflow.js` (OpenCode 1.x) y `.opencode/plugins/bflow2/` (OpenCode 2.x, que además inyecta el estado de la tarea al primer prompt); se commitean y no se editan: el guard antes de bash, edit, write y patch, y los tokens por fase y agente. OpenCode no tiene nudge de subagente; el plugin inyecta `bflow hook session-start` al crear una sesión raíz (sin pedir respuesta), así que un `/clear` no pierde el hilo.

OpenCode pide el modelo como `proveedor/modelo`, así que los alias de los agentes (`opus`, `sonnet`, `haiku`) se traducen con `models.opencode`. Los defaults son `opus` en spec-author y reviewer, `sonnet` en implementer, security-auditor y ux-auditor, y `haiku` en scout y documenter; `agents.<nombre>.model` los cambia. Hay dos lugares, con efectos distintos:

- `bflow.yaml` (equipo): el modelo se escribe en el `model:` de los agentes versionados.

```yaml
models:
  opencode:
    opus: anthropic/claude-opus-4
    sonnet: anthropic/claude-sonnet-4
    haiku: anthropic/claude-haiku-4
```

- `models:` en el nivel superior de la config global (solo tú; este repo no la versiona, ahí mapeas `opus`, `sonnet` y `haiku` con `models: { opencode: { opus: <proveedor/modelo>, ... } }`): nunca entra a los archivos versionados. `bflow render` e `install opencode` lo llevan a `agent.bflow-<agente>.model` de tu `opencode.json` (`$XDG_CONFIG_HOME/opencode` o `~/.config/opencode`), conservando el resto de tu archivo. Si el repo define el alias, gana el repo y no se escribe override. Si solo existe `opencode.jsonc`, o el `opencode.json` no se puede leer, bflow no lo edita: `render` te da el bloque `{"agent": ...}` para pegar. `render --check` no lee ni escribe tu `opencode.json`.
- Los `models` de un perfil se ignoran (`render` y `doctor` lo avisan): ponlos en `bflow.yaml` o en el nivel superior de la global.

Cada `start` guarda en el log el modelo de cada agente. `bflow stats` (línea `modelos:`, y `data.models`) y `bflow doctor` (ítem `veredicto-modelos`) comparan las 3 tareas más recientes con la configuración vigente contra las 3 anteriores: rechazos por gate, rondas y hotfixes. Si algo sube, lo dicen en rojo (solo con color) y nombran el modelo anterior al que volver; para volver, cambia `bflow.yaml` (agents) y corre `bflow render`. bflow nunca lo revierte por su cuenta.

Un valor con `/` se usa tal cual. Un alias sin equivalente en ninguno de los dos deja al agente sin `model:` (usa el de tu OpenCode) y `render` y `bflow doctor` lo avisan; `doctor` también avisa si tu `opencode.json` no está al día (`bflow render`). Los archivos fuente están en [`adapters/opencode/`](../../adapters/opencode/).

## Paneles y notificaciones

- `bflow watch` es un panel en vivo en otra terminal: quién trabaja y desde cuándo, o qué gate espera tu decisión; lo que sigue; tiempos por fase; tokens por agente; fricción, y los últimos eventos. `bflow watch --open` lo abre en otra pestaña o ventana si no hay uno abierto. Con `ui: { watch: true }`, `bflow start` lo abre solo; nunca en CI ni sin escritorio.
- `bflow ui` muestra el panel en el navegador, en una página local de solo lectura: tablero, línea del carril, métricas y todos tus repos agrupados por perfil. Con `ui: { web: true }`, la ventana también se abre al empezar una tarea; si ya hay una abierta, no abre otra.
- Cuando la tarea espera tu decisión (una gate o un bloqueo) y Claude termina su turno, bflow manda una notificación del sistema, una vez por espera. `ui: { notify: false }` la apaga.

Como son preferencias personales, `ui:` va en la config global, no en `bflow.yaml`. El primer `bflow init` en cada máquina pregunta qué abrir (panel y navegador, solo el panel o nada) y lo guarda ahí; no vuelve a preguntar, pero puedes cambiarlo editando `ui:`.
